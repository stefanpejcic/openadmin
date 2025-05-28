################################################################################
# *************************************************************************
# *                                                                       *
# * OpenAdmin                                                             *
# * Copyright (c) OpenPanel. All Rights Reserved.                         *
# * Version: 1.3.3                                                        *
# * Build Date: 2025-05-28 10:37:18                                       *
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
import subprocess

# import our functions
from app import app, cache, is_license_valid, admin_required_route, load_openpanel_config, connect_to_database


# Define file paths
file_paths = {
    'zone_template_ipv4': '/etc/openpanel/bind9/zone_template.txt',
    'zone_template_ipv6': '/etc/openpanel/bind9/zone_template_ipv6.txt'
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



@app.route('/domains/zone-templates', methods=['GET', 'POST'])
@admin_required_route
def edit_dns_zone_templates():
    current_route = request.path

    if request.method == 'POST':
        # Update files with posted content
        zone_template_ipv4 = request.form.get('zone_template_ipv4')
        zone_template_ipv6 = request.form.get('zone_template_ipv6')

        # Write the new content to files
        if zone_template_ipv4 is not None:
            write_file(file_paths['zone_template_ipv4'], zone_template_ipv4)
        if zone_template_ipv6 is not None:
            write_file(file_paths['zone_template_ipv6'], zone_template_ipv6)

        flash("Template updated successfully!", "success")

    file_contents = {}
    for key, path in file_paths.items():
        file_contents[key] = read_file(path) or ''

    if request.args.get('output') == 'json':
        return jsonify(results)
    return render_template('domains/dns_templates.html', title='DNS Zone Templates', current_route=current_route, **file_contents)
