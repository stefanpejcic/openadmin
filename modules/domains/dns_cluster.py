################################################################################
# *************************************************************************
# *                                                                       *
# * OpenAdmin                                                             *
# * Copyright (c) OpenPanel. All Rights Reserved.                         *
# * Version: 1.3.3                                                        *
# * Build Date: 2025-05-28 10:37:26                                       *
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
# Last Modified: 25.04.2024
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
import re
import ipaddress
import subprocess
import fileinput

# import our functions
from app import app, cache, is_license_valid, admin_required_route, load_openpanel_config, connect_to_database

CONFIG_PATH = '/etc/bind/named.conf.options'


def extract_values_from_config(file_path):
    with open(file_path, 'r') as f:
        content = f.read()

    # Detect if directives are present and uncommented
    is_allow_transfer_uncommented = re.search(
        r'^(?!\s*(//|#)).*\ballow-transfer\b', content, re.MULTILINE) is not None
    is_also_notify_uncommented = re.search(
        r'^(?!\s*(//|#)).*\balso-notify\b', content, re.MULTILINE) is not None

    # Remove all comments for clean parsing
    content_no_comments = re.sub(r'//.*?$|/\*.*?\*/|#.*?$',
                                 '', content, flags=re.MULTILINE | re.DOTALL)

    # Extract blocks if any
    allow_transfer_matches = re.findall(r'allow-transfer\s*{\s*([^}]+)\s*};', content_no_comments)
    also_notify_matches = re.findall(r'also-notify\s*{\s*([^}]+)\s*};', content_no_comments)

    def parse_ips(blocks):
        ips = []
        for block in blocks:
            for ip in block.split(';'):
                ip = ip.strip()
                if ip:
                    ips.append(ip)
        return ips

    return {
        'allow_transfer': parse_ips(allow_transfer_matches),
        'also_notify': parse_ips(also_notify_matches),
        'enabled': is_allow_transfer_uncommented and is_also_notify_uncommented,
        'raw_content': content
    }



def add_ip_to_config(file_path, new_ip):
    """
    Adds the new_ip to both allow-transfer and also-notify blocks.
    Assumes the blocks exist and are properly formatted.
    """

    with open(file_path, 'r') as f:
        content = f.read()

    def add_ip_to_block(content, block_name, ip):
        # Regex to find block content inside braces
        pattern = rf'({block_name}\s*{{)([^}}]*)(}};)'
        match = re.search(pattern, content, re.DOTALL)
        if not match:
            raise ValueError(f"{block_name} block not found in config")

        before, block_content, after = match.groups()
        # Extract current ips, ignoring whitespace and semicolons
        ips = [ip.strip() for ip in block_content.split(';') if ip.strip()]

        if ip in ips:
            # Already present, return content unchanged
            return content

        # Add new ip with semicolon and newline for readability
        new_block_content = block_content.strip() + f';\n    {ip}'

        # Replace the block in the content
        updated_block = f"{before}{new_block_content}{after}"
        # Replace the original block with the updated one
        return content[:match.start()] + updated_block + content[match.end():]

    content = add_ip_to_block(content, 'allow-transfer', new_ip)
    content = add_ip_to_block(content, 'also-notify', new_ip)

    with open(file_path, 'w') as f:
        f.write(content)


@app.route('/domains/dns-cluster', methods=['GET', 'POST'])
@admin_required_route
def get_dns_cluster_config():
    current_route = request.path

    if request.method == 'POST':
        action = request.form.get('action')

        if action in ['enable', 'disable']:
            try:
                update_config_file(CONFIG_PATH, enable=(action == 'enable'))
                restart_dns_service()
                flash(f'DNS cluster {action}d successfully.', 'success')
            except Exception as e:
                flash(f'Failed to {action} DNS cluster: {str(e)}', 'error')
        elif action == 'create':
            ip = request.form.get('ip')
            if not ip:
                flash('IP address is required.', 'error')
            else:
                try:
                    # Validate IPv4
                    ip_obj = ipaddress.ip_address(ip)
                    if ip_obj.version != 4:
                        flash('Only IPv4 addresses are currently supported.', 'error')
                        return

                    # Extract current IPs
                    extracted = extract_values_from_config(CONFIG_PATH)
                    all_ips = set(extracted['allow_transfer'] + extracted['also_notify'])

                    if ip in all_ips:
                        flash('IP address already exists in configuration.', 'error')
                    else:
                        add_ip_to_config(CONFIG_PATH, ip)
                        restart_dns_service()
                        flash(f'IP {ip} added to DNS cluster successfully.', 'success')

                except ValueError:
                    flash('Invalid IP address format.', 'error')
                except Exception as e:
                    flash(f'Failed to add IP to DNS cluster: {str(e)}', 'error')

    try:
        data = extract_values_from_config(CONFIG_PATH)
    except Exception as e:
        data = ''
    
    if request.args.get('output') == 'json':
        return jsonify(data)
    return render_template('domains/cluster.html', title='DNS Cluster', current_route=current_route, **data)


def update_config_file(file_path, enable=True):
    with open(file_path, 'r') as f:
        lines = f.readlines()

    new_lines = []
    for line in lines:
        if enable:
            if line.lstrip().startswith('// allow-transfer'):
                new_lines.append(line.replace('// allow-transfer', 'allow-transfer', 1))
            elif line.lstrip().startswith('// also-notify'):
                new_lines.append(line.replace('// also-notify', 'also-notify', 1))
            else:
                new_lines.append(line)
        else:
            if line.lstrip().startswith('allow-transfer'):
                new_lines.append(line.replace('allow-transfer', '// allow-transfer', 1))
            elif line.lstrip().startswith('also-notify'):
                new_lines.append(line.replace('also-notify', '// also-notify', 1))
            else:
                new_lines.append(line)

    with open(file_path, 'w') as f:
        f.writelines(new_lines)



def restart_dns_service():
    subprocess.Popen(["docker", "--context=default", "restart", "openpanel_dns"])
