"""
# *************************************************************************
# *                                                                       *
# * OpenAdmin                                                             *
# * Copyright (c) OpenPanel. All Rights Reserved.                         *
# * Version: 1.7.65                                                        *
# * Build Date: 2026-07-06 18:11:00                                       *
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
generate a list of available api endpoints for enterprise edition
must be used before encoding with pyarmor
"""
import argparse
import os
import re
import subprocess
import requests


def get_server_ip():
    urls = [
        ("https://ip.openpanel.com", "ip.openpanel.com"),
        ("https://ifconfig.me/ip", "ifconfig.me"),
    ]

    for url, name in urls:
        try:
            response = requests.get(url, timeout=1)
            if response.status_code == 200:
                ip = response.text.strip()
                return ip
        except requests.exceptions.RequestException as e:
            print(f"Error checking IPv4 from {name}: {e}")

    caddyfile_path = "/etc/openpanel/caddy/Caddyfile"
    print(f"INIT - Falling back to 'default_bind' in {caddyfile_path}")

    return '127.0.0.1'

def extract_api_info(file_path):
    with open(file_path, 'r') as file:
        lines = file.readlines()

    endpoints_info = []
    endpoint_info = {}

    for line in lines:
        # Check for ENDPOINT comment
        if line.strip().startswith("# ENDPOINT:"):
            if endpoint_info:
                # Save the previous endpoint info before starting a new one
                endpoints_info.append(endpoint_info)
                endpoint_info = {}

            endpoint_info['endpoint'] = line.split(":")[1].strip()

        # Check for DESCRIPTION comment
        elif line.strip().startswith("# DESCRIPTION:"):
            endpoint_info['description'] = line.split(":")[1].strip()

        # Check for TYPE comment
        elif line.strip().startswith("# TYPE:"):
            endpoint_info['type'] = line.split(":")[1].strip()

        # Check for EXAMPLES or EXAMPLE comment
        elif line.strip().startswith("# EXAMPLES:") or line.strip().startswith("# EXAMPLE:"):
            examples = []
            endpoint_info['examples'] = examples  # Initialize examples as an empty list

        # Collect the examples that follow
        elif endpoint_info.get('examples') is not None and line.strip().startswith("#"):
            example = line.lstrip("#").strip()
            if example:
                examples.append(example)

    # Add the last parsed endpoint info
    if endpoint_info:
        endpoints_info.append(endpoint_info)

    return endpoints_info

def format_endpoints_info(endpoints_info, protocol, domain, port):

    output = []
    for endpoint in endpoints_info:
        output.append(f"\nEndpoint: {endpoint['endpoint']}")
        output.append(f"Description: {endpoint['description']}")
        output.append(f"Type: {endpoint['type']}")
        output.append("Examples:")
        for example in endpoint['examples']:
            formatted_example = example.replace('http://localhost:2087', f'{protocol}://{domain}:{port}')
            output.append(f"  {formatted_example}")
        output.append("\n" + "-" * 80)  # Separator line
    return "\n".join(output)

def read_existing_file(file_path, protocol, domain, port):
    if os.path.exists(file_path):
        with open(file_path, 'r') as file:
            content = file.read()
        return content.replace('http://localhost:2087', f'{protocol}://{domain}:{port}')
    else:
        return ""


def get_domain():
    try:
        result = subprocess.run(["opencli", "domain"], capture_output=True, text=True, check=True)
        domain = result.stdout.strip()
        if re.match(r"^\d{1,3}\.\d{1,3}\.\d{1,3}\.\d{1,3}$", domain):
            return None
        return domain
    except subprocess.CalledProcessError as e:
        return None
    except FileNotFoundError:
        return None

def get_openadmin_port():
    try:
        result = subprocess.run(["opencli", "admin", "port"], capture_output=True, text=True, check=True)
        port = result.stdout.strip()
        if not re.match(r"^([0-9]{1,5})$", port) or not (0 <= int(port) <= 65535):
            return None
        return port
    except subprocess.CalledProcessError as e:
        return 2087
    except FileNotFoundError:
        return 2087


def main():
    parser = argparse.ArgumentParser(description="Process API endpoint information.")
    parser.add_argument('--save', action='store_true', help='Save the output to available_endpoints.txt')
    args = parser.parse_args()

    file_path = '/usr/local/admin/modules/api/endpoints.py'
    output_file = '/usr/local/admin/modules/api/available_endpoints.txt'
    config_file_path = '/etc/openpanel/openpanel/conf/openpanel.config'

    force_domain = get_domain()
    server_ip = get_server_ip()
    port = get_openadmin_port()

    if force_domain:
        protocol = 'https'
        domain = force_domain
    else:
        protocol = 'http'
        domain = server_ip
    
    if args.save:
        endpoints_info = extract_api_info(file_path)
        formatted_info = format_endpoints_info(endpoints_info, 'http', 'localhost', port)
        with open(output_file, 'w') as f:
            f.write(formatted_info)
    else:
        # Read from existing file
        existing_info = read_existing_file(output_file, protocol, domain, port)
        if existing_info:
            print(existing_info)
        else:
            print("API.GENERATE - No information available. Run 'opencli api list --save' to generate the file.")

if __name__ == "__main__":
    main()
