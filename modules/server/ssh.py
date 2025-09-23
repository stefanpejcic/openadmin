################################################################################
# *************************************************************************
# *                                                                       *
# * OpenAdmin                                                             *
# * Copyright (c) OpenPanel. All Rights Reserved.                         *
# * Version: 1.6.0                                                        *
# * Build Date: 2025-09-23 11:25:17                                       *
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
# Created: 23.06.2024
# Last Modified: 23.06.2024
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
from flask import Flask, render_template, request, redirect, url_for, flash, jsonify
import subprocess
import re
import os
from functools import wraps

# import our modules
from app import app, admin_required_route

SSHD_CONFIG_PATH = '/etc/ssh/sshd_config'
AUTHORIZED_KEYS_PATH = os.path.expanduser('~/.ssh/authorized_keys')

def is_valid_port(port):
    print(f"SERVER.SSH - Validating port: {port}")
    try:
        port = int(port)
        return 22 <= port <= 10000
    except ValueError:
        return False

def is_valid_auth_param(param):
    print(f"SERVER.SSH - Validating auth param: {param}")
    return param in {'yes', 'no'}

def get_ssh_status():
    print(f"SERVER.SSH - Checking SSH service status with: systemctl is-active ssh")
    result = subprocess.run(['systemctl', 'is-active', 'ssh'], stdout=subprocess.PIPE)
    status = result.stdout.decode('utf-8').strip()
    return status

def get_ssh_config():
    print(f"SERVER.SSH - Reading: {SSHD_CONFIG_PATH}")
    with open(SSHD_CONFIG_PATH, 'r') as file:
        config = file.read()
    return config

def execute_action_for_ssh_service(action):
    command = f"systemctl {action} ssh"
    print(f"SERVER.SSH - Executing: {command}")
    subprocess.run(['systemctl', action, 'ssh'])

def get_ssh_settings():
    print(f"SERVER.SSH - Reading: port, password_auth, pubkey_auth, permit_root_login from {SSHD_CONFIG_PATH}")
    settings = {
        "port": "22",
        "password_auth": "yes",
        "pubkey_auth": "no",
        "permit_root_login": "yes"
    }
    with open(SSHD_CONFIG_PATH, 'r') as file:
        for line in file:
            stripped_line = line.strip()
            if stripped_line.startswith("#Port"):
                settings["port"] = "22"
            elif stripped_line.startswith("Port"):
                settings["port"] = stripped_line.split()[1]
            elif stripped_line.startswith("#PasswordAuthentication"):
                settings["password_auth"] = "no"
            elif stripped_line.startswith("PasswordAuthentication"):
                settings["password_auth"] = stripped_line.split()[1]
            elif stripped_line.startswith("#PubkeyAuthentication"):
                settings["pubkey_auth"] = "no"
            elif stripped_line.startswith("PubkeyAuthentication"):
                settings["pubkey_auth"] = stripped_line.split()[1]
            elif stripped_line.startswith("#PermitRootLogin"):
                settings["permit_root_login"] = "no"
            elif stripped_line.startswith("PermitRootLogin"):
                settings["permit_root_login"] = stripped_line.split()[1]
    return settings


#NASTAVI
def update_ssh_config(new_config):
    print(f"SERVER.SSH - Updating: {SSHD_CONFIG_PATH}")
    with open(SSHD_CONFIG_PATH, 'w') as file:
        file.write(new_config)
    restart_ssh_service()

def restart_ssh_service()
    execute_action_for_ssh_service('restart')
    print(f"SERVER.SSH - Executing: {command}")
    subprocess.run(command)

def update_ssh_settings(settings):
    print(f"SERVER.SSH - Updating SSH port and authentication settings..")

    with open(SSHD_CONFIG_PATH, 'r') as file:
        lines = file.readlines()
    with open(SSHD_CONFIG_PATH, 'w') as file:
        for line in lines:
            stripped_line = line.strip()
            if stripped_line.startswith("#Port") or stripped_line.startswith("Port"):
                file.write(f"Port {settings['port']}\n")
            elif stripped_line.startswith("#PasswordAuthentication") or stripped_line.startswith("PasswordAuthentication"):
                file.write(f"PasswordAuthentication {settings['password_auth']}\n")
            elif stripped_line.startswith("#PubkeyAuthentication") or stripped_line.startswith("PubkeyAuthentication"):
                file.write(f"PubkeyAuthentication {settings['pubkey_auth']}\n")
            elif stripped_line.startswith("#PermitRootLogin") or stripped_line.startswith("PermitRootLogin"):
                file.write(f"PermitRootLogin {settings['permit_root_login']}\n")
            else:
                file.write(line)
    restart_ssh_service()

def get_authorized_keys():
    print(f"SERVER.SSH - Checking for authorized SSH keys in: {AUTHORIZED_KEYS_PATH}")

    keys_with_comments = []
    pending_comment = ""
    
    if os.path.exists(AUTHORIZED_KEYS_PATH):
        with open(AUTHORIZED_KEYS_PATH, 'r') as file:
            for line in file:
                line = line.strip()
                if not line:
                    continue
                if line.startswith("#"):
                    pending_comment = line[1:].strip()  
                else:
                    keys_with_comments.append({
                        "comment": pending_comment,
                        "key": line
                    })
                    pending_comment = ""
    return keys_with_comments

def add_authorized_key(new_key):
    print(f"SERVER.SSH - Adding SSH key to authorized keys file: {AUTHORIZED_KEYS_PATH}")
    with open(AUTHORIZED_KEYS_PATH, 'a') as file:
        file.write(new_key + '\n')

def remove_authorized_key(key_to_remove):
    print(f"SERVER.SSH - Removing SSH key from authorized keys file: {AUTHORIZED_KEYS_PATH}")
    if os.path.exists(AUTHORIZED_KEYS_PATH):
        with open(AUTHORIZED_KEYS_PATH, 'r') as file:
            lines = file.readlines()
        with open(AUTHORIZED_KEYS_PATH, 'w') as file:
            for line in lines:
                stripped = line.strip()
                if stripped and not stripped.startswith('#') and stripped != key_to_remove.strip():
                    file.write(line)
                elif stripped.startswith('#') or not stripped:
                    file.write(line)

@app.route('/server/ssh', methods=['GET', 'POST'])
@admin_required_route
def manage_ssh():
    output_format = request.args.get('output')

    if request.method == 'POST':
        action = request.form.get('action')
        new_config = request.form.get('config')
        new_key = request.form.get('new_key')
        key_to_remove = request.form.get('key_to_remove')
        port = request.form.get('port')
        password_auth = request.form.get('password_auth')
        pubkey_auth = request.form.get('pubkey_auth')
        permit_root_login = request.form.get('permit_root_login')

        # Validate SSH port
        if port and not is_valid_port(port):
            return jsonify({'error': 'Invalid SSH port. It must be a number between 22 and 10000.'}), 400

        # Validate authentication parameters
        if password_auth and not is_valid_auth_param(password_auth):
            return jsonify({'error': 'Invalid value for password_auth. It must be "yes", "no".'}), 400
        if pubkey_auth and not is_valid_auth_param(pubkey_auth):
            return jsonify({'error': 'Invalid value for pubkey_auth. It must be "yes" or "no".'}), 400
        if permit_root_login and not is_valid_auth_param(permit_root_login):
            return jsonify({'error': 'Invalid value for permit_root_login. It must be "yes", "no".'}), 400

        if action:
            execute_action_for_ssh_service(action)
            flash(f'SSH service has been {action}ed.', 'success')

        if new_config:
            update_ssh_config(new_config)
            flash('SSH configuration updated and service restarted.', 'success')

        if new_key:
            add_authorized_key(new_key)
            flash('New SSH key added.', 'success')

        if key_to_remove:
            remove_authorized_key(key_to_remove)
            flash('SSH key removed.', 'success')

        if port or password_auth or pubkey_auth or permit_root_login:
            settings = {
                "port": port,
                "password_auth": password_auth,
                "pubkey_auth": pubkey_auth,
                "permit_root_login": permit_root_login
            }
            update_ssh_settings(settings)
            flash('SSH settings updated.', 'success')

        return redirect(url_for('manage_ssh'))

    # For GET request, render the template with current status, config, and authorized keys
    ssh_status = get_ssh_status()
    ssh_config = get_ssh_config()
    authorized_keys = get_authorized_keys()
    ssh_settings = get_ssh_settings()

    if output_format == 'json':
        return jsonify({
            'status': ssh_status,
            'config': ssh_config,
            'keys': authorized_keys,
            **ssh_settings
        })

    return render_template('server/ssh.html', status=ssh_status, title="SSH", config=ssh_config, keys=authorized_keys, **ssh_settings)

@app.route('/server/ssh/config', methods=['GET', 'POST'])
@admin_required_route
def get_full_config():
    if request.method == 'POST':
        new_config = request.form.get('config')
        if new_config:
            update_ssh_config(new_config)
            flash('SSH configuration updated and service restarted.', 'success')
            return redirect(url_for('manage_ssh'))
    else:
        config = get_ssh_config()
        return jsonify({'config': config})

