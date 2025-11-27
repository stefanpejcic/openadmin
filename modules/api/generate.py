################################################################################
# *************************************************************************
# *                                                                       *
# * OpenAdmin                                                             *
# * Copyright (c) OpenPanel. All Rights Reserved.                         *
# * Version: 1.7.1                                                        *
# * Build Date: 2025-11-27 19:39:28                                       *
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
# Author:   Stefan Pejcic <stefan@pejcic.rs>
# Created:  11.07.2023
# Modified: 10.10.2025
# Project:  www.openpanel.com
# Copyright (c) Stefan Pejcic
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

"""
generate a list of available api endpoints for enterprise edition
must be used before encoding with pyarmor
"""
import argparse
import os
import subprocess

def extract_api_info(file_path):
    print(f"API.GENERATE - Reading: {file_path}")
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
    print(f"API.GENERATE - Formating endpoints information..")

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
        print(f"API.GENERATE - Reading cached endpoints file: {file_path}")
        with open(file_path, 'r') as file:
            content = file.read()
        return content.replace('http://localhost', f'{protocol}://{domain}')
    else:
        print(f"API.GENERATE - No existing file found at {file_path}.")
        return ""

# TODO: not used anymore!
def get_force_domain(config_file_path):
    force_domain = None
    if os.path.exists(config_file_path):
        with open(config_file_path, 'r') as file:
            for line in file:
                if line.strip().startswith("force_domain="):
                    force_domain = line.strip().split("=", 1)[1].strip()
                    break
    return force_domain

def get_server_ip():
    print(f"API.GENERATE - Checking public IPv4 for the server..")

    try:
        print(f"API.GENERATE - Trying: https://ip.openpanel.com")
        result = subprocess.run(
            ['curl', '--silent', '--max-time', '2', '-4', 'https://ip.openpanel.com'],
            capture_output=True,
            text=True,
            timeout=2
        )
        if result.returncode == 0 and result.stdout.strip():
            return result.stdout.strip()
        
        print(f"API.GENERATE - Failed, trying: https://ip.openpanel.com")
        result = subprocess.run(
            ['wget', '--timeout=2', '-qO-', 'https://ipv4.openpanel.com'],
            capture_output=True,
            text=True,
            timeout=2
        )
        if result.returncode == 0 and result.stdout.strip():
            return result.stdout.strip()

        print(f"API.GENERATE - Failed again, trying: https://ifconfig.me")
        result = subprocess.run(
            ['curl', '--silent', '--max-time', '2', '-4', 'https://ifconfig.me'],
            capture_output=True,
            text=True,
            timeout=2
        )
        if result.returncode == 0 and result.stdout.strip():
            return result.stdout.strip()

        print(f"API.GENERATE - All 3 domains are unreachable - do you have internet access or something blocking outgoing port 443?")
        return "127.0.0.1"
    except Exception as e:
        print(f"API.GENERATE - Error retrieving IP address: {e}")
        return "127.0.0.1"

def main():
    parser = argparse.ArgumentParser(description="Process API endpoint information.")
    parser.add_argument('--save', action='store_true', help='Save the output to available_endpoints.txt')
    args = parser.parse_args()

    file_path = 'endpoints.py'
    output_file = '/usr/local/admin/modules/api/available_endpoints.txt'
    config_file_path = '/etc/openpanel/openpanel/conf/openpanel.config'

    force_domain = get_force_domain(config_file_path)
    server_ip = get_server_ip()

    if force_domain:
        protocol = 'https'
        domain = force_domain
    else:
        protocol = 'http'
        domain = server_ip

    if args.save:
        # Extract and format information, then save to file
        endpoints_info = extract_api_info(file_path)
        formatted_info = format_endpoints_info(endpoints_info, 'http', 'localhost')
        with open(output_file, 'w') as f:
            f.write(formatted_info)
        print(f"API.GENERATE - Endpoint information saved to {output_file}")
    else:
        # Read from existing file
        existing_info = read_existing_file(output_file, protocol, domain)
        if existing_info:
            print("API.GENERATE - Available API Endpoints:")
            print(existing_info)
        else:
            print("API.GENERATE - No information available. Run with --save to generate the file.")

if __name__ == "__main__":
    main()
