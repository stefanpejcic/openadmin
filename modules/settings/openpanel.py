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

# dictionary, different than load_openpanel_config in app.py
@cache.memoize(timeout=30)
def load_openpanel_config(config_file_path):
    config_data = {}
    try:
        with open(config_file_path, 'r') as file:
            section_title = None
            for line in file:
                line = line.strip()
                if line.startswith('['):
                    section_title = line.strip('[]')
                    config_data[section_title] = config_data.get(section_title, {})
                elif '=' in line:
                    key, value = line.split('=', 1)
                    if section_title:
                        config_data[section_title][key.strip()] = value.strip()
    except IOError as e:
        logging.error(f"Error reading configuration file: {e}")
    return config_data


def save_openpanel_config(config_data, config_file_path):
    try:
        with open(config_file_path, 'w') as file:
            for section, settings in config_data.items():
                file.write(f'[{section}]\n')
                for key, value in settings.items():
                    file.write(f'{key}={value}\n')
        return True
    except IOError as e:
        logging.error(f"Error writing configuration file: {e}")
        return False


# added in 0.3.3 to auto-start clamav when malware_scan is enabled
def update_clamav_in_docker_compose(enable_clamav):
    if enable_clamav:
        # Uncomment - clamav (keeping spaces)
        os.system("sed -i 's/^\\( *\\)#\\s*-\\s*clamav/\\1- clamav/' /root/docker-compose.yml")
    else:
        # Comment - clamav (keeping spaces)
        os.system("sed -i 's/^\\( *\\)-\\s*clamav/\\1# - clamav/' /root/docker-compose.yml")




@app.route('/settings/open-panel', methods=['GET', 'POST'])
@admin_required_route
def open_panel_settings():
    config_file_path = '/etc/openpanel/openpanel/conf/openpanel.config'
    config_data = load_openpanel_config(config_file_path)

    success_messages = []
    error_messages = []
    openpanel_service_restart_is_needed = False

    if request.method == 'POST':
        form_data = {
            "brand_name": request.form.get('brand_name'),
            "logo": request.files.get('logo'),
            "ns1": request.form.get('ns1'),
            "ns2": request.form.get('ns2'),
            "ns3": request.form.get('ns3'),
            "ns4": request.form.get('ns4'),
            "avatar_type": request.form.get('avatar_type'),
            "resource_usage_charts_mode": request.form.get('resource_usage_charts_mode'),
            "default_php_version": request.form.get('default_php_version'),
            "password_reset": 'password_reset' in request.form,
            "weakpass": 'weakpass' in request.form,
            "twofa_nag": 'twofa_nag' in request.form,
            "how_to_guides": 'how_to_guides' in request.form,
            "logout_url": request.form.get('logout_url'),
            "max_login_records": int(request.form.get('max_login_records')),
            "login_ratelimit": int(request.form.get('login_ratelimit')),
            "login_blocklimit": int(request.form.get('login_blocklimit')),
            "session_duration": int(request.form.get('session_duration')),
            "session_lifetime": int(request.form.get('session_lifetime')),
            "activity_items_per_page": int(request.form.get('activity_items_per_page')),
            "domains_per_page": int(request.form.get('domains_per_page')),
            "resource_usage_retention": int(request.form.get('resource_usage_retention')),
            "resource_usage_items_per_page": int(request.form.get('resource_usage_items_per_page'))
        }

        # Validate and process form data
        valid_values = {
            "avatar_type": ['gravatar', 'icon', 'letter'],
            "resource_usage_charts_mode": ['one', 'two', 'none'],
            "activity_items_per_page": [25, 50, 100, 200],
            "login_ratelimit": [5, 20, 50, 100],
            "login_blocklimit": [5, 20, 50, 100],
            "session_duration": [10, 30, 50, 100],
            "session_lifetime": [60, 300, 600, 1000],
            "resource_usage_items_per_page": [25, 50, 100, 200],
            "resource_usage_retention": [100, 250, 500, 1000],
            "max_login_records": [10, 20, 50, 100],
            "domains_per_page": [100, 250, 500, 1000]
        }

        def validate_value(key, value):
            if key in valid_values:
                if value not in valid_values[key]:
                    error_messages.append(f"Error: '{value}' is not a valid value for {key}.")
                    return None
            return value


        # Update the config_data dictionary with new values
        for key, value in form_data.items():
            if value is not None:
                if key in valid_values:
                    value = validate_value(key, value)
                    if value is None:
                        continue

                # Determine the correct section for the key
                if key in {'brand_name', 'logo', 'ns1', 'ns2', 'ns3', 'ns4', 'logout_url'}:
                    section = 'DEFAULT'
                elif key in {'resource_usage_charts_mode', 'avatar_type'}:
                    section = 'USERS'
                elif key == 'default_php_version':
                    section = 'PHP'
                else:
                    section = 'USERS'  # Default to USERS for other keys

                if section not in config_data:
                    config_data[section] = {}

                config_data[section][key] = value

        # Handle boolean fields specifically
        for key in ["how_to_guides", "twofa_nag", "password_reset", "weakpass"]:
            if 'USERS' not in config_data:
                config_data['USERS'] = {}
                
            current_value = 'yes' if config_data['USERS'].get(key, 'no') == 'yes' else 'no'
            new_value = 'yes' if form_data.get(key, False) else 'no'
            if new_value != current_value:
                config_data['USERS'][key] = new_value

                # Update the config_data dictionary with new values
                for key, value in form_data.items():
                    if value is not None:
                        if key in valid_values:
                            value = validate_value(key, value)
                            if value is None:
                                continue

        # Save the updated configuration
        if save_openpanel_config(config_data, config_file_path):
            openpanel_service_restart_is_needed = True
            success_messages.append("Configuration saved successfully.")
        else:
            error_messages.append("Error saving configuration file.")

        # Restart service if needed
        if openpanel_service_restart_is_needed:
            with open("/root/openpanel_restart_needed", "w") as file:
                file.write("Restart needed for OpenPanel service.")
                # todo on startup gunicorn
                #data_json_path = "/etc/openpanel/openpanel/core/users/*/data.json"
                #for file in glob.glob(data_json_path):
                #    os.remove(file)  # Remove the file                


    current_route = request.path
    config_data = {}
    with open(config_file_path, 'r') as file:
        for line in file:
            line = line.strip()
            if line.startswith('['):
                section_title = line.strip('[]')
            elif line and '=' in line:
                key, value = line.split('=', 1)
                if section_title:
                    if section_title not in config_data:
                        config_data[section_title] = {}
                    config_data[section_title][key] = value

    return render_template('settings/user.html', title='User Panel Settings', current_route=current_route,config_data=config_data, app=app)



@app.route('/features', methods=['GET', 'POST'])
@admin_required_route
def open_panel_enable_features():
    config_file_path = '/etc/openpanel/openpanel/conf/openpanel.config'
    features_json_path = '/etc/openpanel/openadmin/config/features.json'

    if request.method == 'POST':
        #enabled_modules_value = ",".join(request.form.keys())
        enabled_modules_value = ",".join(key for key in request.form.keys() if key != 'csrf_token')

        with open(config_file_path, 'r') as file:
            lines = file.readlines()

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
        with open("/root/openpanel_restart_needed", "w") as file:
            file.write("Restart needed for OpenPanel service.")


    current_route = request.path
    enabled_modules_value = ""

    with open(config_file_path, 'r') as file:
        for line in file:
            line = line.strip()
            if line.startswith("enabled_modules="):
                enabled_modules_value = line.split("=", 1)[1].strip('"')
                break  # Stop reading once we find it

    # Convert the comma-separated string to a list for easy use in templates
    enabled_modules = enabled_modules_value.split(",") if enabled_modules_value else []


    # Load feature definitions
    with open(features_json_path, 'r') as f:
        all_features = json.load(f)

    # Annotate with current status
    for feature in all_features:
        feature['status'] = feature['name'] in enabled_modules

    return render_template('settings/features.html', title='Manage Features', current_route=current_route,enabled_modules=enabled_modules, app=app, features=all_features)

