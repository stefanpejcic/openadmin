################################################################################
# Author: Stefan Pejcic
# Created: 11.07.2023
# Last Modified: 02.08.2024
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




import os
import json
import socket
from flask import Flask, Response, abort, render_template, request, send_file, g, jsonify, session, url_for, flash, redirect, get_flashed_messages
import subprocess
import datetime
import psutil
from app import app, is_license_valid, login_required_route, connect_to_database
import docker

from modules.helpers import get_all_users, get_user_and_plan_count, get_plan_by_id, get_all_plans, get_userdata_by_username, get_hosting_plan_name_by_id, get_user_websites, is_username_unique, gravatar_url


# dictionary, different than load_openpanel_config in app.py
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


@app.route('/settings/open-panel', methods=['GET', 'POST'])
@login_required_route
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
            "enabled_modules": request.form.get('enabled_modules'),
            "resource_usage_charts_mode": request.form.get('resource_usage_charts_mode'),
            "default_php_version": request.form.get('default_php_version'),
            "password_reset": 'password_reset' in request.form,
            "twofa_nag": 'twofa_nag' in request.form,
            "how_to_guides": 'how_to_guides' in request.form,
            "logout_url": request.form.get('logout_url'),
            "max_login_records": int(request.form.get('max_login_records')),
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
                if key in {'brand_name', 'logo', 'ns1', 'ns2', 'ns3', 'ns4', 'enabled_modules', 'logout_url'}:
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
        for key in ["how_to_guides", "twofa_nag", "password_reset"]:
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
            try:
                subprocess.check_output("docker restart openpanel", shell=True, text=True)
                success_messages.append("OpenPanel service restarted to apply changes.")
            except subprocess.CalledProcessError as e:
                error_messages.append(f"Error restarting OpenPanel service: '{e.output}'")

        response_data = {
            "success_messages": success_messages,
            "error_messages": error_messages
        }

        return jsonify(response_data)

    else:
        # If it's a GET request, read the conf file and organize it by sections
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

        return render_template('open_panel_settings.html', title='User Panel Settings', current_route = current_route,config_data=config_data, app=app)

    # none of the above
    return "Unsupported request method.", 405
