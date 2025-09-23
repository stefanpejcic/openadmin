################################################################################
# *************************************************************************
# *                                                                       *
# * OpenAdmin                                                             *
# * Copyright (c) OpenPanel. All Rights Reserved.                         *
# * Version: 1.6.0                                                        *
# * Build Date: 2025-09-23 11:25:03                                       *
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
    print(f"SETTINGS.OPENPANEL - Reading: {config_file_path} (data is cached for 30s)")
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
        print(f"SETTINGS.OPENPANEL - Error reading configuration file: {e}")
    return config_data


def save_openpanel_config(config_data, config_file_path):
    print(f"SETTINGS.OPENPANEL - Saving configuration to: {config_file_path}")
    try:
        with open(config_file_path, 'w') as file:
            for section, settings in config_data.items():
                file.write(f'[{section}]\n')
                for key, value in settings.items():
                    file.write(f'{key}={value}\n')
        return True
    except IOError as e:
        print(f"SETTINGS.OPENPANEL - Error saving to file: {e}")
        return False


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
            "logo": request.form.get('logo'),
            "ns1": request.form.get('ns1'),
            "ns2": request.form.get('ns2'),
            "ns3": request.form.get('ns3'),
            "ns4": request.form.get('ns4'),
            "avatar_type": request.form.get('avatar_type'),
            "resource_usage_charts_mode": request.form.get('resource_usage_charts_mode'),
            "password_reset": request.form.get('password_reset'),
            "permit_username_change_by_user": request.form.get('permit_username_change_by_user'),
            "permit_subdomain_sharing": request.form.get('permit_subdomain_sharing'),
            "twofa_nag": request.form.get('twofa_nag'),
            "how_to_guides": request.form.get('how_to_guides'),
            "found_a_bug_link": request.form.get('found_a_bug_link'),
            "ip_county_flag": request.form.get('ip_county_flag'),
            "weakpass": 'weakpass' in request.form,
            "autopurge_trash": int(request.form.get('autopurge_trash')),
            "filemanager_edit_size": int(request.form.get('filemanager_edit_size')),
            "filemanager_view_size": int(request.form.get('filemanager_view_size')),
            "filemanager_download_size": int(request.form.get('filemanager_download_size')),
            "filemanager_upload_size": int(request.form.get('filemanager_upload_size')),
            "filemanager_compress_max_time": int(request.form.get('filemanager_compress_max_time')),
            "filemanager_download_max_time": int(request.form.get('filemanager_download_max_time')),
            "filemanager_extract_max_time": int(request.form.get('filemanager_extract_max_time')),
            "filemanager_edit_extensions": request.form.get('filemanager_edit_extensions'),
            "filemanager_image_extensions": request.form.get('filemanager_image_extensions'),
            "filemanager_archives_extensions": request.form.get('filemanager_archives_extensions'),
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

        valid_values = {
            "avatar_type": ['gravatar', 'icon', 'letter'],
            "resource_usage_charts_mode": ['one', 'two', 'none'],
            "activity_items_per_page": "non_negative_int",
            "login_ratelimit": "non_negative_int",
            "login_blocklimit": "non_negative_int",
            "session_duration": "non_negative_int",
            "session_lifetime": "non_negative_int",
            "resource_usage_items_per_page": "non_negative_int",
            "resource_usage_retention": "non_negative_int",
            "max_login_records": "non_negative_int",
            "domains_per_page": "non_negative_int",
            "autopurge_trash": "non_negative_int",
            "filemanager_edit_size": "non_negative_int",
            "filemanager_view_size": "non_negative_int",
            "filemanager_download_size": "non_negative_int",
            "filemanager_upload_size": "non_negative_int",
            "filemanager_compress_max_time": "non_negative_int",
            "filemanager_extract_max_time": "non_negative_int",
            "filemanager_download_max_time": "non_negative_int",
            "filemanager_edit_extensions": "space_separated_extensions",
            "filemanager_image_extensions": "space_separated_extensions",
            "filemanager_archives_extensions": "space_separated_extensions",
            "how_to_guides": ['yes', 'no'],
            "found_a_bug_link": ['yes', 'no'],
            "ip_county_flag": ['yes', 'no'],
            "password_reset": ['yes', 'no'],
            "weakpass": ['yes', 'no'],
            "permit_subdomain_sharing": ['yes', 'no'],
            "permit_username_change_by_user": ['yes', 'no']
        }


        def validate_value(key, value):
            print(f"SETTINGS.OPENPANEL - Validating value: '{value}' for key: '{key}'")
            if key in valid_values:
                rule = valid_values[key]
                if rule == "non_negative_int":
                    try:
                        int_value = int(value)
                        if int_value < 0:
                            raise ValueError
                        return int_value
                    except ValueError:
                        print(f"SETTINGS.OPENPANEL - Error: '{value}' must be a non-negative integer for {key}")
                        error_messages.append(f"Error: '{value}' must be a non-negative integer for {key}.")
                        return None
                elif rule == "space_separated_extensions":
                    extensions = value.strip().split()
                    if not all(ext.startswith('.') or ext.isalpha() for ext in extensions):
                        print(f"SETTINGS.OPENPANEL - Error: '{value}' must be space-separated valid file extensions for {key}")
                        error_messages.append(f"Error: '{value}' must be space-separated valid file extensions for {key}.")
                        return None
                    return " ".join(extensions)  # optionally store cleaned string
                elif value not in rule:
                    print(f"SETTINGS.OPENPANEL - Error: '{value}' is not a valid value for {key}")
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
                    print(f"SETTINGS.OPENPANEL - Executing: opencli config update {key} {value}")
                if key in {'brand_name', 'logo', 'ns1', 'ns2', 'ns3', 'ns4', 'logout_url'}:
                    section = 'DEFAULT'
                elif key.startswith('filemanager_') or key == 'autopurge_trash':
                    section = 'FILES'
                else:
                    section = 'USERS'  # Default to USERS for everythng else

                if section not in config_data:
                    config_data[section] = {}

                config_data[section][key] = value

        if save_openpanel_config(config_data, config_file_path):
            openpanel_service_restart_is_needed = True
            success_messages.append("Configuration saved successfully.")
        else:
            error_messages.append("Error saving configuration file.")

        if openpanel_service_restart_is_needed:
            print(f"SETTINGS.OPENPANEL - Creating restart needed flag file for OpenPanel.")
            with open("/root/openpanel_restart_needed", "w") as file:
                file.write("Restart needed for OpenPanel service.")

    current_route = request.path
    config_data = {}
    print(f"SETTINGS.OPENPANEL - Reading: {config_file_path}")
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
                    config_data[section_title][key] = value.strip().strip('"')

    return render_template('settings/openpanel.html', title='User Panel Settings', current_route=current_route,config_data=config_data, app=app)
