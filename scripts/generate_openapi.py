#!/usr/bin/env python3
"""Generates the OpenAPI spec from internal/apidocs/api_endpoints.json, a
hand-curated catalog of every /api/* route registered in cmd/openadmin/main.go
(cross-checked against each handler's actual request/response shape). Writes
two copies:
  - static/openapi.yaml (same-origin copy the in-app Swagger UI at
    /settings/api loads - a relative server URL so it resolves against
    whatever domain/port the panel is actually running on)
  - ../OpenPanel/website/static/openadmin-openapi.yaml (public docs site
    download link, with an example.com placeholder server since it's
    downloaded standalone) -- only written if that path exists locally.

Usage: python3 scripts/generate_openapi.py
Requires: pip install pyyaml

Update internal/apidocs/api_endpoints.json (not the generated YAML) when
routes change, then rerun this script.
"""
import json
import os
import re
from collections import OrderedDict

import yaml

SCRIPT_DIR = os.path.dirname(os.path.abspath(__file__))
REPO_ROOT = os.path.dirname(SCRIPT_DIR)
APIDOCS = os.path.join(REPO_ROOT, "internal", "apidocs", "api_endpoints.json")

APP_OUT = os.path.join(REPO_ROOT, "static", "openapi.yaml")
WEBSITE_OUT = os.path.join(os.path.dirname(REPO_ROOT), "OpenPanel", "website", "static", "openadmin-openapi.yaml")

with open(APIDOCS) as f:
    groups = json.load(f)

WILDCARD_PARAM_RE = re.compile(r'\{(\w+)\.\.\.\}')
PARAM_RE = re.compile(r'\{(\w+)\}')

# auth classification (from cmd/openadmin/main.go's RequireAPI* wrapper
# around each route) -> (requires bearer token?, human-readable requirement)
AUTH_DESCRIPTIONS = {
    "none": (False, "No authentication (this is the login/health-check endpoint itself)."),
    "RequireAPIToken": (True, "Any authenticated user."),
    "RequireAPIAdmin": (True, "Administrator or user role (the reseller role is blocked)."),
    "RequireAPIOwnerOrAdmin": (True, "Administrator/user role, or a reseller who owns the target account."),
    "RequireAPISuperAdmin": (True, "The Super Admin (role must be exactly \"admin\") only."),
}


def to_openapi_path(path):
    # {log_filename...} (a Go 1.22+ ServeMux wildcard segment, can contain
    # slashes) isn't valid OpenAPI path-param syntax -- represent it as a
    # normal {log_filename} param; the per-param description below notes
    # that it may contain slashes.
    return WILDCARD_PARAM_RE.sub(lambda m: '{' + m.group(1) + '}', path)


def path_params(path):
    stripped = WILDCARD_PARAM_RE.sub(lambda m: '{' + m.group(1) + '}', path)
    return PARAM_RE.findall(stripped)


def is_wildcard_param(path, name):
    return f'{{{name}...}}' in path


def json_schema_for_value(v):
    if isinstance(v, bool):
        return {"type": "boolean", "example": v}
    if isinstance(v, int):
        return {"type": "integer", "example": v}
    if isinstance(v, float):
        return {"type": "number", "example": v}
    if isinstance(v, list):
        item_schema = json_schema_for_value(v[0]) if v else {"type": "string"}
        return {"type": "array", "items": item_schema, "example": v}
    if isinstance(v, dict):
        return {"type": "object", "properties": {k: json_schema_for_value(val) for k, val in v.items()}, "example": v}
    return {"type": "string", "example": v}


def body_schema(body):
    if isinstance(body, list):
        return json_schema_for_value(body)
    props = {k: json_schema_for_value(v) for k, v in body.items()}
    return {"type": "object", "properties": props, "example": body}


def response_schema(resp):
    if isinstance(resp, dict):
        props = {k: json_schema_for_value(v) for k, v in resp.items()}
        return {"type": "object", "properties": props, "example": resp}
    if isinstance(resp, str):
        return {"type": "string", "example": resp}
    return {"type": "object", "additionalProperties": True}


def slug_for_path(path):
    slug = PARAM_RE.sub(lambda m: m.group(1), path)
    if slug.startswith('/api/'):
        slug = slug[len('/api/'):]
    slug = slug.strip('/')
    slug = re.sub(r'[/\-.]', '_', slug)
    slug = re.sub(r'[^a-zA-Z0-9_]', '', slug)
    return slug or "root"


def build_spec(same_origin=False):
    paths = OrderedDict()
    tags = []
    seen_tags = set()

    slug_method_count = {}
    for g in groups:
        for e in g['endpoints']:
            oapi_path = to_openapi_path(e['path'])
            slug = slug_for_path(oapi_path)
            slug_method_count[slug] = slug_method_count.get(slug, 0) + 1

    operation_id_used = set()

    for g in groups:
        group_name = g['group']
        if group_name not in seen_tags:
            seen_tags.add(group_name)
            tags.append({"name": group_name})

        for e in g['endpoints']:
            method = e['method'].lower()
            raw_path = e['path']
            oapi_path = to_openapi_path(raw_path)
            auth = e.get('auth', 'RequireAPIToken')
            requires_token, auth_text = AUTH_DESCRIPTIONS[auth]
            description = e['description']

            if oapi_path not in paths:
                paths[oapi_path] = OrderedDict()

            slug = slug_for_path(oapi_path)
            op_id = f"{method}_{slug}" if slug_method_count[slug] > 1 else slug
            base_op_id = op_id
            n = 2
            while op_id in operation_id_used:
                op_id = f"{base_op_id}_{n}"
                n += 1
            operation_id_used.add(op_id)

            operation = OrderedDict()
            operation["summary"] = description
            operation["description"] = f"**Access:** {auth_text}"
            operation["operationId"] = op_id
            operation["tags"] = [group_name]

            parameters = []
            for p in path_params(raw_path):
                param = {
                    "name": p, "in": "path", "required": True,
                    "schema": {"type": "string"},
                }
                if is_wildcard_param(raw_path, p):
                    param["description"] = "May contain '/' - this segment matches the rest of the path."
                parameters.append(param)
            if 'params' in e:
                for qp in re.findall(r'[?&]([A-Za-z0-9_]+)=', e['params']):
                    parameters.append({
                        "name": qp, "in": "query", "required": False,
                        "schema": {"type": "string"},
                    })
            if parameters:
                operation["parameters"] = parameters

            if "body" in e:
                operation["requestBody"] = {
                    "required": True,
                    "content": {"application/json": {"schema": body_schema(e["body"])}},
                }

            success_response = {
                "description": "Success.",
                "content": {"application/json": {"schema": response_schema(e.get("response", {}))}},
            }
            operation["responses"] = OrderedDict([
                ("200", success_response),
                ("default", {"$ref": "#/components/responses/Error"}),
            ])

            operation["security"] = [{"bearerAuth": []}] if requires_token else []

            paths[oapi_path][method] = operation

    spec = OrderedDict()
    spec["openapi"] = "3.0.3"
    spec["info"] = OrderedDict([
        ("title", "OpenAdmin API"),
        ("version", "1.0.0"),
        ("description",
         "REST API for OpenAdmin, the administrator-level control panel for OpenPanel server management. "
         "Every feature available in OpenAdmin's web UI can also be accessed programmatically.\n\n"
         "All endpoints require a Bearer token obtained from `POST /api/` (username/password login), except "
         "the login call itself and the `GET /api/` health check. Tokens expire after 15 minutes. Beyond "
         "authentication, most endpoints also require the administrator role -- see each operation's "
         "**Access** note for its exact requirement (some allow a reseller who owns the target account, and a "
         "handful of the most dangerous server-control actions require the Super Admin role specifically).\n\n"
         "The whole API is also gated behind [PANEL] api=on and a configured license key -- when either is "
         "off, every /api/* route (including this one) responds as if it doesn't exist.\n\n"
         "Success responses are JSON objects (`200`) unless noted otherwise. Error responses always include "
         "an `error` field and use standard HTTP status codes (400, 401, 403, 404, 409, 500)."),
        ("contact", OrderedDict([("name", "OpenPanel"), ("url", "https://openpanel.com")])),
        ("license", OrderedDict([("name", "OpenPanel EULA"), ("url", "https://openpanel.com/LICENSE")])),
    ])
    if same_origin:
        spec["servers"] = [
            {"url": "/", "description": "This OpenAdmin installation"},
        ]
    else:
        spec["servers"] = [
            {"url": "https://panel.example.com:2087", "description": "Your OpenAdmin installation (default port 2087)"},
        ]
    spec["tags"] = tags
    spec["paths"] = paths
    spec["components"] = OrderedDict([
        ("securitySchemes", OrderedDict([
            ("bearerAuth", OrderedDict([
                ("type", "http"),
                ("scheme", "bearer"),
                ("bearerFormat", "JWT"),
                ("description", "Obtain a token via `POST /api/` (username/password login). Tokens expire after 15 minutes."),
            ])),
        ])),
        ("responses", OrderedDict([
            ("Error", OrderedDict([
                ("description", "An error occurred. See the `error` field for details."),
                ("content", OrderedDict([
                    ("application/json", OrderedDict([
                        ("schema", OrderedDict([
                            ("type", "object"),
                            ("properties", OrderedDict([
                                ("error", {"type": "string", "example": "Forbidden: administrator role required"}),
                            ])),
                            ("required", ["error"]),
                        ])),
                    ])),
                ])),
            ])),
        ])),
    ])
    spec["security"] = [{"bearerAuth": []}]
    return spec


class NoAliasDumper(yaml.SafeDumper):
    def ignore_aliases(self, data):
        return True


def represent_ordereddict(dumper, data):
    return dumper.represent_mapping('tag:yaml.org,2002:map', data.items())


def represent_str(dumper, data):
    if '\n' in data:
        return dumper.represent_scalar('tag:yaml.org,2002:str', data, style='|')
    return dumper.represent_scalar('tag:yaml.org,2002:str', data)


NoAliasDumper.add_representer(OrderedDict, represent_ordereddict)
NoAliasDumper.add_representer(str, represent_str)


def write_spec(out_path, spec):
    os.makedirs(os.path.dirname(out_path), exist_ok=True)
    with open(out_path, 'w') as f:
        f.write("# Auto-generated from internal/apidocs/api_endpoints.json by scripts/generate_openapi.py.\n")
        f.write("# To update: regenerate rather than hand-editing this file directly.\n")
        yaml.dump(spec, f, Dumper=NoAliasDumper, sort_keys=False, allow_unicode=True, default_flow_style=False, width=100)
    print(f"Wrote {out_path}")


def main():
    app_spec = build_spec(same_origin=True)
    write_spec(APP_OUT, app_spec)

    if os.path.isdir(os.path.dirname(WEBSITE_OUT)):
        website_spec = build_spec(same_origin=False)
        write_spec(WEBSITE_OUT, website_spec)
    else:
        print(f"Skipped {WEBSITE_OUT} (../OpenPanel/website/static not found locally)")

    total_ops = sum(len(v) for v in app_spec["paths"].values())
    print(f"paths: {len(app_spec['paths'])}, operations: {total_ops}, tags: {len(app_spec['tags'])}")


if __name__ == "__main__":
    main()
