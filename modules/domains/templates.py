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
import re
import subprocess

# import our functions
from app import app, cache, is_license_valid, admin_required_route, load_openpanel_config, connect_to_database


# Define file paths
file_paths = {
    'default_page': '/etc/openpanel/nginx/default_page.html',
    'suspended_user': '/etc/openpanel/nginx/suspended_user.html',
    'suspended_website': '/etc/openpanel/nginx/suspended_website.html',
    'docker_nginx_domain': '/etc/openpanel/nginx/vhosts/1.1/docker_nginx_domain.conf',
    'docker_openresty_domain': '/etc/openpanel/nginx/vhosts/1.1/docker_openresty_domain.conf',
    'docker_apache_domain': '/etc/openpanel/nginx/vhosts/1.1/docker_apache_domain.conf'
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



@app.route('/domains/file-templates', methods=['GET', 'POST'])
@admin_required_route
def edit_domain_files_templates():
    current_route = request.path

    if request.method == 'POST':
        # Update files with posted content
        default_page = request.form.get('default_page')
        suspended_user = request.form.get('suspended_user')
        suspended_website = request.form.get('suspended_website')
        docker_nginx_domain = request.form.get('docker_nginx_domain')
        docker_openresty_domain = request.form.get('docker_openresty_domain')
        docker_apache_domain = request.form.get('docker_apache_domain')

        # Write the new content to files
        if default_page is not None:
            write_file(file_paths['default_page'], default_page)
        if suspended_user is not None:
            write_file(file_paths['suspended_user'], suspended_user)
        if suspended_website is not None:
            write_file(file_paths['suspended_website'], suspended_website)
        if docker_nginx_domain is not None:
            write_file(file_paths['docker_nginx_domain'], docker_nginx_domain)
        if docker_openresty_domain is not None:
            write_file(file_paths['docker_openresty_domain'], docker_openresty_domain)            
        if docker_apache_domain is not None:
            write_file(file_paths['docker_apache_domain'], docker_apache_domain)
        flash("Template updated successfully!", "success")

    file_contents = {}
    for key, path in file_paths.items():
        file_contents[key] = read_file(path) or ''

    if request.args.get('output') == 'json':
        return jsonify(results)
    return render_template('domains/templates.html', title='Domains Templates', current_route=current_route, **file_contents)
