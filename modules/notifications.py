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
# Last Modified: 20.08.2024
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




# import python modules
import os
from flask import Flask, Response, render_template, request, g, jsonify, session, redirect, url_for, flash, abort
import subprocess

# import our modules
from app import app, cache, admin_required_route 

# helper function, should be moved to modules.helpers
@cache.memoize(timeout=300)
def load_openpanel_config(config_file_path):
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
    return config_data

# acknowledge notification
@app.route('/mark_as_read/<int:line_number>', methods=['POST'])
@admin_required_route
def mark_notification_as_read(line_number):
    log_dir = "/var/log/openpanel/admin"
    log_file = os.path.join(log_dir, 'notifications.log')

    try:
        with open(log_file, 'r') as f:
            lines = f.readlines()

        command = request.form.get('command', '')

        if command == 'mark_all_as_read':
            # Mark all notifications as READ
            lines = [line.replace('UNREAD', 'READ') for line in lines]
        elif 1 <= line_number <= len(lines):
            # Mark a specific notification as READ from the bottom
            lines[-line_number] = lines[-line_number].replace('UNREAD', 'READ')
        else:
            return abort(400, "Invalid line number")

        with open(log_file, 'w') as f:
            f.writelines(lines)

        return redirect(url_for('view_notifications'))

    except FileNotFoundError:
        return abort(400, "Log file not found")
    except Exception as e:
        return abort(500, f"Error marking notification as read: {e}")

@app.route('/view_notifications', methods=['GET', 'POST'])
@admin_required_route
def view_notifications():
    config_file_path = '/etc/openpanel/openadmin/config/notifications.ini'
    config_data = load_openpanel_config(config_file_path)

    def update_notification(setting, current_value, new_value, command_template):
        if new_value != current_value:
            command = command_template.format(new_value)
            success_message = f"{setting} value changed."
            error_message = f"Error: {setting} value could not be saved."
            try:
                result = subprocess.check_output(command, shell=True, text=True)
                if f"Updated {setting.lower()} to" in result:
                    success_messages.append(success_message)
                    return True
                else:
                    error_messages.append(error_message)
            except subprocess.CalledProcessError as e:
                error_messages.append(f"Error executing command: '{command}': {e.output}")
        return False

    if request.method == 'POST':
        # Get form data
        settings = {
            'reboot': request.form.get('reboot'),
            'email': request.form.get('email'),
            'login': request.form.get('login'),
            'attack': request.form.get('attack'),
            'limit': request.form.get('limit'),
            'update': request.files.get('update'),
            'load': int(request.form.get('load')),
            'cpu': int(request.form.get('cpu')),
            'ram': int(request.form.get('ram')),
            'du': int(request.form.get('du')),
            'swap': int(request.form.get('swap')),
            'services': request.form.get('services')
        }

        # Current values from config
        current_values = {key: int(config_data.get('DEFAULT', {}).get(key, 0)) for key in ['load', 'cpu', 'ram', 'du', 'swap']}

        # Success and error message lists
        success_messages = []
        error_messages = []
        openpanel_service_restart_is_needed = False

        # Update thresholds (load, cpu, ram, etc.)
        for setting, value in settings.items():
            if setting in current_values and value != current_values[setting]:
                command_template = f"opencli admin notifications update {setting} '{{}}'"
                if update_notification(setting, current_values[setting], value, command_template):
                    openpanel_service_restart_is_needed = True

        # Handle services and other settings that require update
        if settings['services']:
            command = f"opencli admin notifications update services '{settings['services']}'"
            success_message = "Notification preferences for services are saved."
            error_message = "Error: Notification preferences for services could not be saved."
            try:
                result = subprocess.check_output(command, shell=True, text=True)
                if "Updated services to" in result:
                    success_messages.append(success_message)
                else:
                    error_messages.append(error_message)
            except subprocess.CalledProcessError as e:
                error_messages.append(f"Error executing command: '{command}': {e.output}")

        toggle_settings = ['update', 'attack', 'limit', 'login', 'reboot']
        for toggle in toggle_settings:
            new_value = settings.get(toggle, 'off')  # Default to 'off' if not in form
            new_value = 'yes' if new_value == 'on' else 'no'
            command = f"opencli admin notifications update {toggle} {new_value}"
            error_message = f"Error: Notifications for {toggle} could not be {'enabled' if settings[toggle] == 'yes' else 'disabled'}."
            try:
                result = subprocess.check_output(command, shell=True, text=True)
                if f"Updated {toggle} to" in result:
                    success_messages.append(success_message)
                else:
                    error_messages.append(error_message)
            except subprocess.CalledProcessError as e:
                error_messages.append(f"Error executing command: '{command}': {e.output}")
    # GET
    main_config_file_path = '/etc/openpanel/openpanel/conf/openpanel.config'
    config_data = load_openpanel_config(main_config_file_path)
    email_address = config_data.get('DEFAULT', {}).get('email', '')

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

    log_dir = "/var/log/openpanel/admin"
    log_file = os.path.join(log_dir, 'notifications.log')

    notifications = None

    try:
        if os.path.exists(log_file):
            with open(log_file, 'r') as f:
                notifications = [line.strip() for line in f.readlines() if line.strip()]
            # reverse
            notifications.sort(reverse=True)
        else:
            # create
            with open(log_file, 'w'):
                pass
    except Exception as e:
        return f"Error loading notifications: {e}"


    output_param = request.args.get('output')
    if output_param == 'json':
        return jsonify({'email_address': email_address, 'settings': config_data, 'notifications': notifications,})


    return render_template('notifications.html', title='Notifications', email_address=email_address, config_data=config_data, notifications=notifications)

