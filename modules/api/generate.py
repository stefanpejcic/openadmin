"""
# *************************************************************************
# *                                                                       *
# * OpenAdmin                                                             *
# * Copyright (c) OpenPanel. All Rights Reserved.                         *
# * Version: 1.7.44                                                        *
# * Build Date: 2026-02-20 23:43:42                                       *
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

def format_endpoints_info(endpoints_info, protocol, domain):

    output = []
    for endpoint in endpoints_info:
        output.append(f"\nEndpoint: {endpoint['endpoint']}")
        output.append(f"Description: {endpoint['description']}")
        output.append(f"Type: {endpoint['type']}")
        output.append("Examples:")
        for example in endpoint['examples']:
            formatted_example = example.replace('http://localhost', f'{protocol}://{domain}')
            output.append(f"  {formatted_example}")
        output.append("\n" + "-" * 80)  # Separator line
    return "\n".join(output)

def read_existing_file(file_path, protocol, domain):
    if os.path.exists(file_path):
        with open(file_path, 'r') as file:
            content = file.read()
        return content.replace('http://localhost', f'{protocol}://{domain}')
    else:
        return ""


def get_openpanel_domain():
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




def get_server_ip():

    try:
        result = subprocess.run(
            ['curl', '--silent', '--max-time', '2', '-4', 'https://ip.openpanel.com'],
            capture_output=True,
            text=True,
            timeout=2
        )
        if result.returncode == 0 and result.stdout.strip():
            return result.stdout.strip()
        
        result = subprocess.run(
            ['wget', '--timeout=2', '-qO-', 'https://ipv4.openpanel.com'],
            capture_output=True,
            text=True,
            timeout=2
        )
        if result.returncode == 0 and result.stdout.strip():
            return result.stdout.strip()

        result = subprocess.run(
            ['curl', '--silent', '--max-time', '2', '-4', 'https://ifconfig.me'],
            capture_output=True,
            text=True,
            timeout=2
        )
        if result.returncode == 0 and result.stdout.strip():
            return result.stdout.strip()

        return "127.0.0.1"
    except Exception as e:
        return "127.0.0.1"

def main():
    parser = argparse.ArgumentParser(description="Process API endpoint information.")
    parser.add_argument('--save', action='store_true', help='Save the output to available_endpoints.txt')
    args = parser.parse_args()

    file_path = '/usr/local/admin/modules/api/endpoints.py'
    output_file = '/usr/local/admin/modules/api/available_endpoints.txt'
    config_file_path = '/etc/openpanel/openpanel/conf/openpanel.config'

    force_domain = get_openpanel_domain()
    server_ip = get_server_ip()

    if force_domain:
        protocol = 'https'
        domain = force_domain
    else:
        protocol = 'http'
        domain = server_ip
    
    if args.save:
        endpoints_info = extract_api_info(file_path)
        formatted_info = format_endpoints_info(endpoints_info, 'http', 'localhost')
        with open(output_file, 'w') as f:
            f.write(formatted_info)
    else:
        # Read from existing file
        existing_info = read_existing_file(output_file, protocol, domain)
        if existing_info:
            print(existing_info)
        else:
            print("API.GENERATE - No information available. Run 'opencli api list --save' to generate the file.")

if __name__ == "__main__":
    main()
