################################################################################
# *************************************************************************
# *                                                                       *
# * OpenAdmin                                                             *
# * Copyright (c) OpenPanel. All Rights Reserved.                         *
# * Version: 1.6.0                                                        *
# * Build Date: 2025-09-23 11:22:08                                       *
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
import os
from flask import Flask, Response, render_template, request, g, jsonify, session, redirect, url_for, flash, abort
import subprocess

# import our modules
from app import app, admin_required_route 

# helper function, should be moved to modules.helpers
def load_openpanel_config(config_file_path):
    print(f"NOTIFICATIONS - Reading: {config_file_path}")
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
@app.route('/notifications/mark_as_read/<int:line_number>', methods=['POST'])
@admin_required_route
def mark_notification_as_read(line_number):
    log_dir = "/var/log/openpanel/admin"
    log_file = os.path.join(log_dir, 'notifications.log')

    try:
        with open(log_file, 'r') as f:
            lines = f.readlines()

        command = request.form.get('command', '')

        if command == 'mark_all_as_read':
            print(f"NOTIFICATIONS - Acknowledging all notifications")
            lines = [line.replace('UNREAD', 'READ') for line in lines]
        elif 1 <= line_number <= len(lines):
            print(f"NOTIFICATIONS - Acknowledging notification ID: {line_number}")
            lines[-line_number] = lines[-line_number].replace('UNREAD', 'READ')
        else:
            print(f"NOTIFICATIONS - Aborted acknowledging notification: invalid ID provided.")
            return abort(400, "Invalid line number")

        with open(log_file, 'w') as f:
            f.writelines(lines)

        return redirect(url_for('view_notifications'))

    except FileNotFoundError:
        print(f"NOTIFICATIONS - Aborted acknowledging notification: {log_file} does not exist.")
        return abort(400, "Log file not found")
    except Exception as e:
        print(f"NOTIFICATIONS - Error acknowledging notification: {e}")
        return abort(500, f"Error marking notification as read: {e}")


# helpers for post email routing options!
import re
import shlex
from markupsafe import escape

def is_valid_port(value):
    print(f"NOTIFICATIONS - Validating port: {value}")
    return str(value).isdigit() and 1 <= int(value) <= 65535

def is_valid_bool(value):
    print(f"NOTIFICATIONS - Validating boolian: {value}")
    return str(value) in ['True', 'False']

def is_valid_email(value):
    print(f"NOTIFICATIONS - Validating email: {value}")
    return re.match(r"^[^@]+@[^@]+\.[^@]+$", value) is not None

def run_command_and_check(command, expected_phrase):
    print(f"NOTIFICATIONS - Executing: {command}")
    try:
        result = subprocess.check_output(command, shell=True, text=True)
        if expected_phrase in result:
            success = True
        else:
            error = True
    except subprocess.CalledProcessError:
        error = True

def update_config_if_valid(key, value, validation_func=None):
    if validation_func is None or validation_func(value):
        safe_val = shlex.quote(str(value))
        cmd = f"opencli config update {key} {safe_val}"
        print(f"NOTIFICATIONS - Executing: {cmd}")
        run_command_and_check(cmd, f"Updated {key} to")
    else:
        print(f"NOTIFICATIONS - Skipping {key}: invalid value '{value}'")



@app.route('/settings/notifications', methods=['GET', 'POST'])
@admin_required_route
def settings_notifications():
    config_file_path = '/etc/openpanel/openadmin/config/notifications.ini'
    config_data = load_openpanel_config(config_file_path)

    if request.method == 'POST':
        config_data = load_openpanel_config(config_file_path)
        current_values = {key: int(config_data.get('DEFAULT', {}).get(key, 0)) for key in ['load', 'cpu', 'ram', 'du', 'swap']}

        # Get form data
        settings = {
            'reboot': request.form.get('reboot'),
            'email': request.form.get('email'),

            'mail_server': request.form.get('mail_server'),
            'mail_port': request.form.get('mail_port'),
            'mail_use_tls': request.form.get('mail_use_tls'),
            'mail_debug': request.form.get('mail_debug'),
            'mail_use_ssl': request.form.get('mail_use_ssl'),
            'mail_username': request.form.get('mail_username'),
            'mail_password': request.form.get('mail_password'),
            'mail_default_sender': request.form.get('mail_default_sender'),

            'login': request.form.get('login'),
            'attack': request.form.get('attack'),
            'limit': request.form.get('limit'),
            'update': request.form.get('update'),
            'load': int(request.form.get('load')),
            'cpu': int(request.form.get('cpu')),
            'ram': int(request.form.get('ram')),
            'du': int(request.form.get('du')),
            'swap': int(request.form.get('swap')),
            'services': request.form.get('services')
        }

        success = False
        error = False

        # Threshold settings (load, cpu, ram, etc.)
        for setting in ['load', 'cpu', 'ram', 'du', 'swap']:
            if settings[setting] != current_values[setting]:
                val = shlex.quote(str(settings[setting]))
                cmd = f"opencli admin notifications update {setting} {val}"
                run_command_and_check(cmd, f"Updated {setting} to")

        # Email
        email = settings.get('email', '')
        val = shlex.quote(email) if email else "''"
        cmd = f"opencli config update email {val}"
        run_command_and_check(cmd, f"Updated email to")

        if settings.get('mail_server'):
            update_config_if_valid("mail_server", settings['mail_server'])

        if 'mail_port' in settings:
            update_config_if_valid("mail_port", settings['mail_port'], is_valid_port)

        if 'mail_use_tls' in settings:
            update_config_if_valid("mail_use_tls", settings['mail_use_tls'], is_valid_bool)


        if 'mail_debug' in settings:
            update_config_if_valid("mail_debug", settings['mail_debug'], is_valid_bool)


        if 'mail_use_ssl' in settings:
            update_config_if_valid("mail_use_ssl", settings['mail_use_ssl'], is_valid_bool)

        if 'mail_username' in settings:
            update_config_if_valid("mail_username", settings['mail_username'], is_valid_email)

        if 'mail_password' in settings:
            update_config_if_valid("mail_password", settings['mail_password'])

        if 'mail_default_sender' in settings:
            update_config_if_valid("mail_default_sender", settings['mail_default_sender'], is_valid_email)

        # Services
        if settings['services']:
            val = shlex.quote(settings['services'])
            cmd = f"opencli admin notifications update services {val}"
            run_command_and_check(cmd, "Updated services to")

        # Toggles
        toggle_settings = ['update', 'attack', 'limit', 'login', 'reboot']
        for toggle in toggle_settings:
            new_value = settings.get(toggle, 'off')
            new_value = 'yes' if new_value == 'on' else 'no'
            cmd = f"opencli admin notifications update {toggle} {new_value}"
            run_command_and_check(cmd, f"Updated {toggle} to")

        # Flash final result
        if success:
            flash("Notification settings updated successfully.", "success")
        if error:
            flash("Some settings could not be saved.", "error")
            
    main_config_file_path = '/etc/openpanel/openpanel/conf/openpanel.config'
    config_data = load_openpanel_config(main_config_file_path)
    email_address = config_data.get('DEFAULT', {}).get('email', '')

    mail_server = config_data.get('SMTP', {}).get('mail_server', '')
    mail_port = config_data.get('SMTP', {}).get('mail_port', '465')
    mail_use_tls = config_data.get('SMTP', {}).get('mail_use_tls', '')
    mail_use_ssl = config_data.get('SMTP', {}).get('mail_use_ssl', '')
    mail_debug = config_data.get('SMTP', {}).get('mail_debug', '')
    mail_username = config_data.get('SMTP', {}).get('mail_username', '')
    mail_password = config_data.get('SMTP', {}).get('mail_password', '')
    mail_default_sender = config_data.get('SMTP', {}).get('mail_default_sender', '')

    config_data = {}
    print(f"NOTIFICATIONS - Reading: {config_file_path}")
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

    output_param = request.args.get('output')
    if output_param == 'json':
        return jsonify({'email_address': email_address,
            'mail_server': mail_server,
            'mail_port': mail_port,
            'mail_use_tls': mail_use_tls,
            'mail_debug': mail_debug,
            'mail_use_ssl': mail_use_ssl,
            'mail_username': mail_username,
            'mail_password': mail_password,
            'mail_default_sender': mail_default_sender,
            'settings': config_data})

    return render_template('settings/notifications.html', title='Notification Settings', mail_server=mail_server, mail_port=mail_port, mail_use_ssl=mail_use_ssl, mail_use_tls=mail_use_tls, mail_debug=mail_debug, mail_username=mail_username, mail_password=mail_password, mail_default_sender=mail_default_sender, config_data=config_data, email_address=email_address)



@app.route('/notifications', methods=['GET'])
@admin_required_route
def view_notifications():
    config_file_path = '/etc/openpanel/openadmin/config/notifications.ini'
    config_data = load_openpanel_config(config_file_path)

    log_dir = "/var/log/openpanel/admin"
    log_file = os.path.join(log_dir, 'notifications.log')

    notifications = None

    try:
        if os.path.exists(log_file):
            print(f"NOTIFICATIONS - Reading notifications from file: {log_file}")
            with open(log_file, 'r') as f:
                notifications = [line.strip() for line in f.readlines() if line.strip()]
            notifications.sort(reverse=True)
        else:
            print(f"NOTIFICATIONS - Creating notifications file: {log_file}")
            with open(log_file, 'w'):
                pass
    except Exception as e:
        return f"OTIFICATIONS - Error loading notifications: {e}"


    output_param = request.args.get('output')
    if output_param == 'json':
        return jsonify(notifications)

    return render_template('notifications.html', title='Notifications', notifications=notifications)
