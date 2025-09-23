################################################################################
# *************************************************************************
# *                                                                       *
# * OpenAdmin                                                             *
# * Copyright (c) OpenPanel. All Rights Reserved.                         *
# * Version: 1.6.0                                                        *
# * Build Date: 2025-09-23 11:17:39                                       *
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
# Created: 11.07.2023
# Last Modified: 21.10.2024
# Company: OPENPANEL
# Copyright (c) openpanel.co
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



import re
import os
import json
import socket
from flask import Flask, Response, abort, render_template, request, send_file, g, jsonify, session, url_for, flash, redirect, get_flashed_messages
import subprocess
import datetime
import psutil
from app import app, cache, is_license_valid, admin_required_route, connect_to_database
import glob



# added in 0.3.3 to auto-start clamav when malware_scan is enabled
def update_clamav_in_docker_compose(enable_clamav):
    if enable_clamav:
        # Uncomment - clamav (keeping spaces)
        print(f"MODULES - Enabling ClamAV in /root/docker-compose.yml")
        os.system("sed -i 's/^\\( *\\)#\\s*-\\s*clamav/\\1- clamav/' /root/docker-compose.yml")
    else:
        # Comment - clamav (keeping spaces)
        print(f"MODULES - Disabling ClamAV in /root/docker-compose.yml")
        os.system("sed -i 's/^\\( *\\)-\\s*clamav/\\1# - clamav/' /root/docker-compose.yml")






def parse_plugin_readme(filepath):
    """Parse simple key=value pairs from readme.txt."""
    metadata = {}
    print(f"MODULES - Parsing plugin metadata from: {filepath}")
    with open(filepath, encoding='utf-8') as f:
        for line in f:
            line = line.strip()
            if not line or line.startswith('#'):
                continue
            if '=' in line:
                key, val = line.split('=', 1)
                metadata[key.strip()] = val.strip()
    return metadata


def get_all_plugins(base_dir='/etc/openpanel/modules/'):
    plugins = []
    print(f"MODULES - Checking for custom plugins in: {base_dir}")
    for plugin_folder in os.listdir(base_dir):
        folder_path = os.path.join(base_dir, plugin_folder)
        readme_txt = os.path.join(folder_path, 'readme.txt')
        if os.path.isdir(folder_path) and os.path.isfile(readme_txt):
            meta = parse_plugin_readme(readme_txt)
            meta['folder'] = plugin_folder
            plugins.append(meta)
    return plugins

'''
@app.route('/plugins')
def plugins_page():
    plugins = get_all_plugins()
    return render_template('plugins.html', plugins=plugins)
'''


@app.route('/settings/modules', methods=['GET', 'POST'])
@admin_required_route
def open_panel_enable_modules():
    config_file_path = '/etc/openpanel/openpanel/conf/openpanel.config'
    features_json_path = '/etc/openpanel/openadmin/config/features.json'

    if request.method == 'POST':
        #enabled_modules_value = ",".join(request.form.keys())
        enabled_modules_value = ",".join(key for key in request.form.keys() if key != 'csrf_token')

        print(f"MODULES - Reading existing enabled modules from: {config_file_path}")
        with open(config_file_path, 'r') as file:
            lines = file.readlines()

        print(f"MODULES - Updating enabled modules in: {config_file_path}")
        with open(config_file_path, 'w') as file:
            for line in lines:
                if line.startswith("enabled_modules="):
                    file.write(f'enabled_modules="{enabled_modules_value}"\n')
                else:
                    file.write(line)

        # Determine the correct section for the key
        key = 'enabled_modules',
        section = 'DEFAULT'

        if 'malware_scan' in enabled_modules_value:
            update_clamav_in_docker_compose(enable_clamav=True)
        else:
            update_clamav_in_docker_compose(enable_clamav=False)

        # Mark OpenPanel for restart
        print(f"MODULES - Creating restart needed flag for OpenPanel..")
        with open("/root/openpanel_restart_needed", "w") as file:
            file.write("Restart needed for OpenPanel service.")


    current_route = request.path
    enabled_modules_value = ""

    print(f"MODULES - Reading enabled modules from: {config_file_path}")
    with open(config_file_path, 'r') as file:
        for line in file:
            line = line.strip()
            if line.startswith("enabled_modules="):
                enabled_modules_value = line.split("=", 1)[1].strip('"')
                break  # Stop reading once we find it

    # Convert the comma-separated string to a list for easy use in templates
    enabled_modules = enabled_modules_value.split(",") if enabled_modules_value else []


    print(f"MODULES - Loading all available modules from : {features_json_path}")
    with open(features_json_path, 'r') as f:
        all_features = json.load(f)

    print(f"MODULES - Checking statuses for modules..")
    for feature in all_features:
        is_enabled = feature['name'] in enabled_modules
        feature['status'] = is_enabled

    if request.method == 'GET' and request.args.get('output') == 'json':
        return jsonify({
            'features': all_features,
            'plugins': get_all_plugins()
        })



    return render_template(
        'settings/modules.html',
        title='Manage Modules',
        current_route=current_route,
        app=app,
        features=all_features,
        plugins=get_all_plugins()
    )


