################################################################################
# *************************************************************************
# *                                                                       *
# * OpenAdmin                                                             *
# * Copyright (c) OpenPanel. All Rights Reserved.                         *
# * Version: 1.6.0                                                        *
# * Build Date: 2025-09-23 11:19:11                                       *
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
# Created: 22.04.2024
# Last Modified: 22.04.2024
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
    'custom_css': '/etc/openpanel/openpanel/custom_code/custom.css',
    'custom_js': '/etc/openpanel/openpanel/custom_code/custom.js',
    'in_header': '/etc/openpanel/openpanel/custom_code/in_header.html',
    'in_footer': '/etc/openpanel/openpanel/custom_code/in_footer.html',
    'post_update': '/root/openpanel_run_after_update',
    'custom_section': '/etc/openpanel/openpanel/conf/custom_dashboard_section.json',
    'forbidden_usernames': '/etc/openpanel/openadmin/config/forbidden_usernames.txt',
    'restricted_domains': '/etc/openpanel/openpanel/conf/domain_restriction.txt',
    'howto_guides': '/etc/openpanel/openpanel/conf/knowledge_base_articles.json',
    'wp_themes': '/etc/openpanel/wordpress/sets/themes.txt',
    'wp_plugins': '/etc/openpanel/wordpress/sets/plugins.txt',
    'pagespeed_api_key': '/etc/openpanel/openpanel/service/pagespeed.api'
}

# Function to read file content
def read_file(file_path):
    print(f"CUSTOM_CODE - Reading: {file_path}")
    if os.path.exists(file_path):
        with open(file_path, 'r') as f:
            return f.read()
    else:
        return None

# Function to write content to a file
def write_file(file_path, content):
    print(f"CUSTOM_CODE - Writing to: {file_path}")
    with open(file_path, 'w') as f:
        f.write(content)



@app.route('/settings/custom-code', methods=['GET', 'POST'])
@admin_required_route
def custom_code():
    current_route = request.path

    if request.method == 'POST':
        # Update files with posted content
        custom_css = request.form.get('custom_css')
        custom_js = request.form.get('custom_js')
        in_header = request.form.get('in_header')
        in_footer = request.form.get('in_footer')
        post_update = request.form.get('post_update')
        custom_section = request.form.get('custom_section')
        howto_guides  = request.form.get('howto_guides')
        forbidden_usernames = request.form.get('forbidden_usernames')
        restricted_domains = request.form.get('restricted_domains')

        wp_themes = request.form.get('wp_themes')
        wp_plugins = request.form.get('wp_plugins')
        pagespeed_api_key  = request.form.get('pagespeed_api_key')

        # Write the new content to files
        if custom_css is not None:
            write_file(file_paths['custom_css'], custom_css)
        if custom_js is not None:
            write_file(file_paths['custom_js'], custom_js)
        if in_header is not None:
            write_file(file_paths['in_header'], in_header)
        if in_footer is not None:
            write_file(file_paths['in_footer'], in_footer)
        if post_update is not None:
            write_file(file_paths['post_update'], post_update)
        if custom_section is not None:
            write_file(file_paths['custom_section'], custom_section)
        if howto_guides is not None:
            write_file(file_paths['howto_guides'], howto_guides)
        if forbidden_usernames is not None:
            write_file(file_paths['forbidden_usernames'], forbidden_usernames)
        if restricted_domains is not None:
            write_file(file_paths['restricted_domains'], restricted_domains)

        if wp_plugins is not None:
            write_file(file_paths['wp_plugins'], wp_plugins)
        if wp_themes is not None:
            write_file(file_paths['wp_themes'], wp_themes)
        if pagespeed_api_key is not None:
            write_file(file_paths['pagespeed_api_key'], pagespeed_api_key)


        # restart services only when needed!
        print(f"CUSTOM_CODE - Adding 'restart needed' flag for OpenPanel..")
        file_path = '/root/openpanel_restart_needed'
        with open(file_path, 'w') as f:
            f.write("Restart needed") 

        flash("Files updated successfully!", "success")

    file_contents = {}
    for key, path in file_paths.items():
        file_contents[key] = read_file(path) or ''

    if request.args.get('output') == 'json':
        return jsonify(file_contents)
    return render_template('settings/custom_code.html', title='Custom Code', current_route=current_route, **file_contents)
