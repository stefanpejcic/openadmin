################################################################################
# Author: Stefan Pejcic
# Created: 11.07.2023
# Last Modified: 24.03.2024
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
from app import app, is_license_valid, login_required_route, load_openpanel_config, connect_to_database
import docker

from modules.helpers import get_all_users, get_user_and_plan_count, get_plan_by_id, get_all_plans, get_userdata_by_username, get_hosting_plan_name_by_id, get_user_websites, is_username_unique, gravatar_url

# import users from login.py
from ..login import User






def run_command(command):
    """Helper function to run a shell command and return its output."""
    try:
        result = subprocess.run(command, stdout=subprocess.PIPE, stderr=subprocess.PIPE, check=True, text=True, shell=True)
        return {'success': True, 'output': result.stdout}
    except subprocess.CalledProcessError as e:
        return {'success': False, 'error': e.stderr}

@app.route('/settings/open-admin/users', methods=['GET', 'POST', 'PATCH', 'DELETE'])
@login_required_route
def manage_users():
    if request.method == 'GET':
        return jsonify(run_command("opencli admin list"))

    elif request.method == 'POST':
        data = request.json
        username = data.get('username')
        password = data.get('password')
        if username and password:
            return jsonify(run_command(f"opencli admin new {username} {password}"))
        else:
            return jsonify({'success': False, 'error': 'Username and password required'}), 400

    elif request.method == 'PATCH':
        data = request.json
        action = data.get('action')
        username = data.get('username')
        
        if action == 'reset_password':
            new_password = data.get('new_password')
            if username and new_password:
                return jsonify(run_command(f"opencli admin password {username} {new_password}"))
            else:
                return jsonify({'success': False, 'error': 'Username and new password required'}), 400
        
        elif action == 'rename_user':
            new_username = data.get('new_username')
            if username and new_username:
                return jsonify(run_command(f"opencli admin rename {username} {new_username}"))
            else:
                return jsonify({'success': False, 'error': 'Current and new username required'}), 400
        
        elif action == 'suspend_user':
            if username:
                return jsonify(run_command(f"opencli admin suspend {username}"))
            else:
                return jsonify({'success': False, 'error': 'Username required'}), 400
        
        elif action == 'unsuspend_user':
            if username:
                return jsonify(run_command(f"opencli admin unsuspend {username}"))
            else:
                return jsonify({'success': False, 'error': 'Username required'}), 400
        else:
            return jsonify({'success': False, 'error': 'Invalid action'}), 400

    elif request.method == 'DELETE':
        data = request.json
        username = data.get('username')
        if username:
            return jsonify(run_command(f"opencli admin delete {username}"))
        else:
            return jsonify({'success': False, 'error': 'Username required'}), 400

    else:
        return jsonify({'success': False, 'error': 'Method not allowed'}), 405




@app.route('/settings/open-admin', methods=['GET', 'POST'])
@login_required_route
def open_admin_settings():
    config_file_path = '/etc/openpanel/openpanel/conf/openpanel.config'

    if request.method == 'POST':
        success_messages = []
        error_messages = []

        action = request.form.get('action')
        if action == "admin_off":
            command = "opencli admin off"
            success_message = "OpenAdmin successfully disabled"
            error_message = f"Error: OpenAdmin service could not be disabled."
            try:
                result = subprocess.check_output(command, shell=True, text=True)
                if "Disabling the AdminPanel" in result:
                    return redirect('/settings/open-admin')
                else:
                    return redirect('/settings/open-admin')
            except subprocess.CalledProcessError as e:
                return jsonify({"message": f"Error executing command: '{command}': {e.output}"})
            return jsonify({"message": "OpenAdmin successfully disabled"})

        elif action == "server_info":
            command = "opencli report --public"
            error_message = "Generating report failed. Please try running from the terminal: 'opencli report'."
            try:
                result = subprocess.check_output(command, shell=True, text=True)
                if "collected successfully" in result:
                    success_messages.append(result)
                    #return redirect('/settings/open-admin')
                else:
                    error_messages.append(error_message)
                    #return redirect('/settings/open-admin')
            except subprocess.CalledProcessError as e:
                #return redirect('/settings/open-admin')
                error_messages.append(f"Error executing command: '{command}': {e.output}")
        #elif action == "enable_features":
        # TODO: save settings using opencli config commands.
            
            '''
        elif action = "admin_new":
            command = "opencli admin new <username> <password>"
        elif action = "admin_password":
            command = "opencli admin password <new_password> [username | admin]"
        elif action = "admin_rename":
            command = "opencli admin rename <old_username> <new_username>"
        elif action = "admin_delete":
            command = "opencli admin delete <username>"

        return redirect('/settings/open-admin')
        '''


        response_data = {}
        if success_messages:
            response_data["success_messages"] = success_messages
        if error_messages:
            response_data["error_messages"] = error_messages

        if response_data:
            return jsonify(response_data)


    #get only
    else:
        current_route = request.path
        # check if api is enabled
        config_data = load_openpanel_config(config_file_path)
        api_status_current_value = config_data.get('PANEL', {}).get('api', 'on')
        basicauth_current_value = config_data.get('PANEL', {}).get('basic_auth', 'no')
        basic_auth_username = config_data.get('PANEL', {}).get('basic_auth_username', '')
        basic_auth_password = config_data.get('PANEL', {}).get('basic_auth_password', '')

        try:
            # Query all users from the 'user' table
            users = User.query.all()

            user_info = []
            for user in users:
                user_data = {
                    'username': user.username,
                    'is_active': user.is_active,
                    'role': user.role
                }
                user_info.append(user_data)

            return render_template('admin_panel_settings.html', title='OpenAdmin Settings', user_info=user_info, current_route=current_route, app=app, api_status_current_value=api_status_current_value, basicauth_current_value=basicauth_current_value, basic_auth_username=basic_auth_username, basic_auth_password=basic_auth_password)
        except Exception as e:
            # Handle any exceptions (e.g., file not found, incorrect format)
            error_message = f"Error reading users from config file: {e}"
            return render_template('error.html', title='Error', error_message=error_message)
            

