################################################################################
# *************************************************************************
# *                                                                       *
# * OpenAdmin                                                             *
# * Copyright (c) OpenPanel. All Rights Reserved.                         *
# * Version: 1.6.0                                                        *
# * Build Date: 2025-09-23 11:22:08                                       *
# *                                                                       *
# *************************************************************************
# *                                                                       *
# * Email: info@openpanel.com                                             *
# * Website: https://openpanel.com                                        *
# *                                                                       *
# *************************************************************************
# *                                                                       *
# * This software is furnished under a license and may be used and copied *
# * only  in  accordance  with  the  terms  of such  license and with the *
# * inclusion of the above copyright notice.  This software  or any other *
# * copies thereof may not be provided or otherwise made available to any *
# * other person.  No title to and  ownership of the software is  hereby *
# * transferred.                                                          *
# *                                                                       *
# * You may not reverse  engineer, decompile, defeat  license  encryption *
# * mechanisms, or  disassemble this software product or software product *
# * license.  OpenPanel may terminate this license if you don't comply    *
# * with any of the terms and conditions set forth in our end user        *
# * license agreement (EULA).  In such event,  licensee  agrees to return *
# * licensor  or destroy  all copies of software  upon termination of the *
# * license.                                                              *
# *                                                                       *
# * Please see the EULA file for the full End User License Agreement.     *
# *                                                                       *
# *************************************************************************
# Author: Stefan Pejcic
# Created: 25.04.2024
# Last Modified: 03.05.2024
# Company: OPENPANEL
# Copyright (c) openpanel.com
# 
# Permission is hereby granted, free of charge, to any person obtaining a copy
# of this software and associated documentation files (the "Software"), to deal
# in the Software without restriction, including without limitation the rights
# to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
# copies of the Software, and to permit persons to whom the Software is
# furnished to do so, subject to the following conditions:
# 
# The above copyright notice and this permission notice shall be included in
# all copies or substantial portions of the Software.
# 
# THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
# IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
# FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
# AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
# LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
# OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN
# THE SOFTWARE.
################################################################################


# import python modules
from flask import Flask, Response, abort, render_template, request, send_file, g, jsonify, session, url_for, flash, redirect, get_flashed_messages, render_template_string

import requests
import os
import tempfile
import re
import subprocess

# import our functions
from app import app, cache, is_license_valid, admin_required_route, load_openpanel_config, connect_to_database

from modules.helpers import query_context_by_username


#from modules.users import read_env_values

global_env_path = '/etc/openpanel/docker/compose/1.0/.env'

def read_env_values():
    print(f"DEFAULTS - Reading variables from: {global_env_path}")
    grouped_values = {'DEFAULTS': {}}
    exclude_keys = {
        'USERNAME', 'USER_ID', 'CONTEXT', 'TOTAL_CPU', 'TOTAL_RAM', 'PIDS',
        'OS', 'HOSTNAME', 'OS_CPU', 'OS_RAM', 'OS_PIDS',
        'BUSYBOX_RAM', 'BUSYBOX_CPU'
    }

    php_fpm_pattern = re.compile(r'^PHP_FPM_(\d+)_(\d+)_(.+)$')

    try:
        with open(global_env_path, 'r') as file:
            for line in file:
                raw_line = line
                line = line.strip()
                if not line or line.startswith('#') or '=' not in line:
                    continue

                key, value = line.split('=', 1)
                key = key.strip()
                value = value.strip().strip('"').strip("'")

                if key in exclude_keys or (key.endswith('_PORT') and key != 'PROXY_HTTP_PORT') or key.endswith('_PW') or key.endswith('_PASSWORD') or key.endswith('_USER'):
                    continue

                # Handle VARNISH (even if commented)
                if 'PROXY_HTTP_PORT' in raw_line and raw_line.strip().startswith('#'):
                    grouped_values['DEFAULTS']['VARNISH'] = False
                    continue
                elif key == 'PROXY_HTTP_PORT':
                    grouped_values['DEFAULTS']['VARNISH'] = True
                    continue

                # Handle DEFAULTS group
                if key == 'WEB_SERVER':
                    grouped_values['DEFAULTS']['WEB_SERVER'] = value
                    continue
                elif key == 'DEFAULT_PHP_VERSION':
                    grouped_values['DEFAULTS']['PHP_VERSION'] = value
                    continue
                elif key == 'MYSQL_TYPE':
                    grouped_values['DEFAULTS']['MYSQL_TYPE'] = value
                    continue

                # Handle special PHP_FPM versioned keys
                match = php_fpm_pattern.match(key)
                if match:
                    major, minor, subkey = match.groups()
                    version = f"{major}.{minor}"
                    if 'PHP_FPM' not in grouped_values:
                        grouped_values['PHP_FPM'] = {}
                    if version not in grouped_values['PHP_FPM']:
                        grouped_values['PHP_FPM'][version] = {}
                    grouped_values['PHP_FPM'][version][subkey] = value
                    continue

                # Generic key grouping
                parts = key.split('_', 1)
                prefix, suffix = parts if len(parts) == 2 else (parts[0], '')
                if prefix not in grouped_values:
                    grouped_values[prefix] = {}
                grouped_values[prefix][suffix] = value

    except FileNotFoundError:
        return None

    return grouped_values


@cache.memoize(timeout=3600)
def get_php_watch_stat():
    '''
    Fetch PHP version data from the API
    '''
    api_url = "https://php.watch/api/v1/versions"
    print(f"DEFAULTS - Fetching data from: {api_url}")

    try:
        response = requests.get(api_url, timeout=3)
        if response.status_code == 200:
            api_data = response.json().get('data', {})
            php_versions_data = {
                v['name']: {
                    'statusLabel': v['statusLabel'],
                    'isEOLVersion': v['isEOLVersion'],
                    'isSecureVersion': v['isSecureVersion'],
                    'isLatestVersion': v['isLatestVersion'],
                    'isFutureVersion': v['isFutureVersion'],
                    'isNextVersion': v['isNextVersion'],
                    'releaseDate': v['releaseDate'],
                    'activeSupportEndDate': v['activeSupportEndDate'],
                    'eolDate': v['eolDate']
                } for v in api_data.values()
            }
        else:
            php_versions_data = {}
    except requests.exceptions.Timeout:
        print("S - The request timed out after 3 seconds.")
        php_versions_data = {}

    return php_versions_data



@app.route('/settings/defaults', methods=['GET', 'POST'])
@admin_required_route
def edit_defaults_for_new_users():
    current_route = request.path
    
    if request.method == 'POST':
        try:
            print(f"DEFAULTS - Reading: {global_env_path}")
            with open(global_env_path, 'r') as file:
                lines = file.readlines()
        except FileNotFoundError:
            flash("Environment file not found.", "error")
            return redirect(request.url)

        new_lines = []
        varnish_enabled = request.form.get('VARNISH', '').strip() == '1'

        for line in lines:
            stripped = line.strip()
            # Skip lines that don't look like key=value
            if not stripped or stripped.startswith('#') or '=' not in line:
                new_lines.append(line)
                continue

            key, old_value = line.split('=', 1)
            key = key.strip()
            old_value = old_value.strip()
            form_key = key

            if form_key in request.form:
                new_value = request.form[form_key].strip()

                if key.endswith('_RAM') and new_value.isdigit() and new_value != '0':
                    new_value = f"{new_value}G"

                new_value = f'"{new_value}"'

                new_lines.append(f"{key}={new_value}\n")
                if key == 'VARNISH':
                    varnish_enabled = new_value.strip('"') == '1'
            else:
                new_lines.append(line)

        final_lines = []
        for line in new_lines:
            if 'PROXY_HTTP_PORT=' in line or 'PROXY_HTTP_PORT' in line:
                uncommented_line = line.lstrip('#').strip()
                if '=' in uncommented_line:
                    if varnish_enabled:
                        final_lines.append(uncommented_line + '\n')
                    else:
                        final_lines.append(f"#{uncommented_line}\n")
                else:
                    final_lines.append(line)
            else:
                final_lines.append(line)

        try:
            print(f"DEFAULTS - Updating: {global_env_path}")
            with open(global_env_path, 'w') as file:
                file.writelines(final_lines)
            ####flash(final_lines, "success")
            flash(f"New defaults saved successfully!", "success")

        except Exception as e:
            flash(f"Failed to update defaults: {str(e)}", "error")

    defaults = read_env_values()
    php_versions_data = get_php_watch_stat()

    if request.args.get('output') == 'json':
        return jsonify(defaults)
    return render_template('settings/defaults.html', title='Edit defaults', current_route=current_route, defaults=defaults, php_versions_data=php_versions_data)



#  NASTAVI

# git source
remote_urls = {
    'compose': 'https://raw.githubusercontent.com/stefanpejcic/openpanel-configuration/refs/heads/main/docker/compose/1.0/docker-compose.yml',
    'env': 'https://raw.githubusercontent.com/stefanpejcic/openpanel-configuration/refs/heads/main/docker/compose/1.0/.env'
}

# local paths
file_paths = {
    'compose': '/etc/openpanel/docker/compose/1.0/docker-compose.yml',
    'env': '/etc/openpanel/docker/compose/1.0/.env'
}


# Function to read file content
def read_file(file_path):
    if os.path.exists(file_path):
        with open(file_path, 'r') as f:
            return f.read()
    else:
        return None

# Function to write content to a file
def write_file(file_path, content):
    with open(file_path, 'w') as f:
        f.write(content)


@app.route('/settings/defaults/files', methods=['GET', 'POST', 'PUT', 'DELETE'])
@admin_required_route
def edit_defaults_files_for_new_users():
    current_route = request.path

    if request.method == 'POST':
        env = request.form.get('env')
        compose = request.form.get('compose')

        if env is not None:
            write_file(file_paths['env'], env)
        if compose is not None:
            write_file(file_paths['compose'], compose)

        flash("Files updated successfully!", "success")

    elif request.method == 'PUT':
        env = request.form.get('env')
        compose = request.form.get('compose')

        tmp_dir = '/tmp/user_defaults'
        os.makedirs(tmp_dir, exist_ok=True)
        env_path = os.path.join(tmp_dir, '.env')
        compose_path = os.path.join(tmp_dir, 'docker-compose.yml')

        try:
            if env is not None:
                with open(env_path, 'w') as f:
                    f.write(env)
            if compose is not None:
                with open(compose_path, 'w') as f:
                    f.write(compose)


            # Populate supporting files
            support_files = {
                "/etc/openpanel/mysql/user.cnf": "custom.cnf",
                "/etc/openpanel/nginx/user-nginx.conf": "nginx.conf",
                "/etc/openpanel/openresty/nginx.conf": "openresty.conf",
                "/etc/openpanel/apache/httpd.conf": "httpd.conf",
                "/etc/openpanel/varnish/default.vcl": "default.vcl",
                "/etc/openpanel/ofelia/users.ini": "crons.ini",
                "/etc/openpanel/backups/backup.env": "backup.env",
                "/etc/openpanel/mysql/phpmyadmin/pma.php": "pma.php",
                "/etc/openpanel/php/ini": "php.ini",
            }

            for src, dst in support_files.items():
                dst_path = os.path.join(tmp_dir, dst)
                if os.path.isdir(src):
                    subprocess.run(['cp', '-r', src, dst_path], check=False)
                else:
                    subprocess.run(['cp', src, dst_path], check=False)

            # Create dummy socket structure
            os.makedirs(os.path.join(tmp_dir, 'sockets', 'mysqld'), exist_ok=True)
            os.makedirs(os.path.join(tmp_dir, 'sockets', 'postgres'), exist_ok=True)
            os.makedirs(os.path.join(tmp_dir, 'sockets', 'redis'), exist_ok=True)
            os.makedirs(os.path.join(tmp_dir, 'sockets', 'memcached'), exist_ok=True)




            result = subprocess.run(
                ['docker', 'compose', '-f', compose_path, 'config'],
                cwd=tmp_dir,
                env={**os.environ, 'COMPOSE_FILE': compose_path},
                stdout=subprocess.PIPE,
                stderr=subprocess.PIPE,
                text=True
            )

            return jsonify({
                'success': result.returncode == 0,
                'stdout': result.stdout,
                'stderr': result.stderr,
                'exit_code': result.returncode
            }), 200 if result.returncode == 0 else 400

        except Exception as e:
            return jsonify({'error': str(e)}), 500

    elif request.method == 'DELETE':
        try:
            all_success = True
            for key in file_paths:
                response = requests.get(remote_urls[key])
                if response.status_code == 200:
                    with open(file_paths[key], 'w') as f:
                        f.write(response.text)
                else:
                    all_success = False
                    flash(f"Failed to fetch {key} file from Github. Status code: {response.status_code}", "error")
            if all_success:
                flash("Defaults reset successfully from remote source!", "success")
        except Exception as e:
            flash(f"Failed to update defaults: {str(e)}", "error")

    file_contents = {}
    for key, path in file_paths.items():
        file_contents[key] = read_file(path) or ''

    if request.args.get('output') == 'json':
        return jsonify(file_contents)

    return render_template('settings/defaults_templates.html',
                           title='Edit defaults Templates',
                           current_route=current_route,
                           **file_contents)






# used on single users > services
@app.route('/settings/defaults/files/<username>', methods=['GET', 'POST'])
@admin_required_route
def edit_files_for_a_user(username):
    context = query_context_by_username(username)

    file_paths = {
        'compose': f'/home/{context}/docker-compose.yml',
        'env': f'/home/{context}/.env'
    }

    if request.method == 'POST':
        env = request.form.get('env')
        compose = request.form.get('compose')

        if env is not None:
            write_file(file_paths['env'], env)
        if compose is not None:
            write_file(file_paths['compose'], compose)

    file_contents = {}
    for key, path in file_paths.items():
        file_contents[key] = read_file(path) or ''

    return jsonify(file_contents)
