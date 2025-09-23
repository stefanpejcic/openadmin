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
from app import app, admin_required_route
from modules.helpers import get_all_plans
# import users from login.py
from ..login import User

reseller_config_path = "/etc/openpanel/openadmin/resellers"


def run_command(args):
    print(f"SETTINGS.RESELLERS - Executing: {args}")    
    try:
        result = subprocess.run(args, stdout=subprocess.PIPE, stderr=subprocess.PIPE, check=True, text=True)
        return {'success': True, 'output': result.stdout.strip()}
    except subprocess.CalledProcessError as e:
        return {'success': False, 'error': e.stderr.strip() or 'Unknown error'}
    except FileNotFoundError as e:
        return {'success': False, 'error': str(e)}


@app.route('/resellers', methods=['GET', 'POST'])
@admin_required_route
def users_resellers():
    current_route = request.path
    log_file = "/var/log/openpanel/admin/login.log"
 
    if request.method == 'POST':
        action = request.form.get('action')
        username = request.form.get('username')
        password = request.form.get('password')

        actions = {
            'create': [
                "opencli", "admin", "new", username, password, "--reseller"
            ],
            'reset_password': [
                "opencli", "admin", "password", username,
                request.form.get('new_password')
            ],
            'rename_user': [
                "opencli", "admin", "rename", username,
                request.form.get('new_username')
            ],
            'update': [
                "opencli", "admin", "update", username,
                f"--allowed_plans={request.form.get('allowed_plans')}",
                f"--max_accounts={request.form.get('max_accounts')}"
            ],
            'suspend': [
                "opencli", "admin", "suspend", username
            ],
            'unsuspend': [
                "opencli", "admin", "unsuspend", username
            ],
            'delete': [
                "opencli", "admin", "delete", username
            ]
        }
    
        if action in actions and username:
            print(f"SETTINGS.RESELLERS - Executing: {actions[action]}")
            result = run_command(actions[action])
            message = result["output"].splitlines()[0] if result["success"] else result["error"].splitlines()[0]
            flash(f'Success: {message}' if result['success'] else f'Error: {message}', 'success' if result['success'] else 'error')
        else:
            print(f"SETTINGS.RESELLERS - Missing required fields")
            flash('Error: Missing required fields.', 'error')

    try:
        # Query all users from the 'user' table
        users = User.query.all()
        user_info = []

        # Read last login info from log file if it exists
        last_login_info = {}
        if os.path.exists(log_file):
            with open(log_file, 'r') as f:
                lines = f.readlines()
                for line in lines:
                    parts = line.strip().split()
                    if len(parts) == 4:
                        date, time, username, ip = parts
                        last_login_info[username] = {
                            'last_ip': ip,
                            'last_login': f"{date} {time}"
                        }

        # Populate user data
        for user in users:
            if user.role.lower() != "reseller":
                continue

            # Default reseller data
            reseller_data = {
                "max_accounts": "N/A",
                "current_accounts": "N/A",
                "allowed_plans": []
            }

            # Read reseller JSON file if it exists
            reseller_file = os.path.join(reseller_config_path, f"{user.username}.json")
            print(f"SETTINGS.RESELLERS - Reading: {reseller_file}")
            if os.path.exists(reseller_file):
                try:
                    with open(reseller_file, 'r') as f:
                        reseller_data = json.load(f)
                except json.JSONDecodeError:
                    reseller_data = {
                        "max_accounts": "Invalid JSON",
                        "current_accounts": "Invalid JSON",
                        "allowed_plans": []
                    }

            user_data = {
                'username': user.username,
                'is_active': user.is_active,
                'role': user.role,
                'last_ip': last_login_info.get(user.username, {}).get('last_ip', 'N/A'),
                'last_login': last_login_info.get(user.username, {}).get('last_login', 'N/A'),
                'max_accounts': reseller_data.get("max_accounts", "N/A"),
                'current_accounts': reseller_data.get("current_accounts", "N/A"),
                'allowed_plans': reseller_data.get("allowed_plans", [])
            }

            user_info.append(user_data)

        if request.args.get('output') == 'json':
            return jsonify(user_info)

        return render_template(
            'users/resellers.html',
            title='Resellers',
            user_info=user_info,
            current_route=current_route,
            app=app
        )
    except Exception as e:
        error_message = f"Error loading resellers: {e}"
        print(f"SETTINGS.RESELLERS - {error_message}")
        return render_template('system/error.html', title='Error', error_message=error_message)


@app.route('/resellers/<action>/<username>')
@admin_required_route
def users_resellers_edit(action, username):
    current_route = request.path
    
    if action not in ['rename', 'update', 'password']:
        flash(f"Error: invalid action, only 'rename', 'update' and 'password' are permited.", 'error')
        print(f"SETTINGS.RESELLERS - Aborting: invalid action, only 'rename', 'update' and 'password' are permited.")
        return redirect('/resellers')

    user = User.query.filter_by(username=username).first()

    if not user or user.role != 'reseller':
        if not user:
            message = f'Error: Reseller {username} does not exist!'
        else:
            message = 'Error: Administrator users cannot be edited!'

        print(f"SETTINGS.RESELLERS - Aborting: {message}")
        flash(message, 'error')
        return redirect('/resellers')

    user_data = {
        "username": user.username,
        "is_active": user.is_active,
        "role": user.role
    }

    if action == 'rename':
        return render_template('users/rename_reseller.html', title=f'Rename Reseller {username}', user_data=user_data, current_route=current_route, app=app)
    elif action == 'password':
        return render_template('users/password_reseller.html', title=f'Change Password for Reseller {username}', user_data=user_data, current_route=current_route, app=app)
    elif action == 'update':

        # Read reseller JSON file if it exists
        reseller_file = os.path.join(reseller_config_path, f"{username}.json")
        print(f"SETTINGS.RESELLERS - Reading: {reseller_file}")
        if os.path.exists(reseller_file):
            try:
                with open(reseller_file, 'r') as f:
                    reseller_data = json.load(f)
            except json.JSONDecodeError:
                reseller_data = {
                    "max_accounts": "Invalid JSON",
                    "current_accounts": "Invalid JSON",
                    "allowed_plans": []
                }

        # get all plans and user count
        hosting_plans = get_all_plans()
        plans = hosting_plans if hosting_plans else []

        return render_template('users/update_reseller.html', title=f'Edit Reseller {username}', plans=plans, user_data=user_data, reseller_data=reseller_data, current_route=current_route, app=app)
