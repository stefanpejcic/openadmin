# import python modules
# *************************************************************************
# *                                                                       *
# * OpenAdmin                                                             *
# * Copyright (c) OpenPanel. All Rights Reserved.                         *
# * Version: 1.3.2                                                        *
# * Build Date: 2025-05-27 19:36:31                                       *
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
import os
import re
import json
from flask import Flask, Response, render_template, request, g, jsonify, session, redirect, url_for, flash
import subprocess
import datetime
from collections import OrderedDict
import psutil
from subprocess import run, Popen, PIPE
import requests
from requests.exceptions import RequestException, HTTPError, ConnectionError, Timeout


# import our modules
from app import app, admin_required_route, load_openpanel_config
from modules.helpers import get_all_users, get_user_and_plan_count, get_all_plans



# CPANEL 2 OPENPANEL IMPORT

def is_pid_running(pid):
    """Check if a process with the given PID is running."""
    try:
        # Using 'ps -p <pid>' to check if the PID is running
        subprocess.check_output(['ps', '-p', str(pid)])
        return True
    except subprocess.CalledProcessError:
        return False

@app.route('/import/cpanel', methods=['GET', 'POST'])
@admin_required_route
def import_users_from_cpanel():
    if request.method == 'GET':
        log_dir = '/var/log/openpanel/admin/imports/'
        log_files = []
        statuses = {}
        now = datetime.datetime.now()

        try:
            files = [f for f in os.listdir(log_dir) if f.endswith('.log')]
        except (FileNotFoundError, PermissionError):
            files = []

        for log_file in files:
            log_file_path = os.path.join(log_dir, log_file)
            status = "unknown"
            try:
                with open(log_file_path, 'r') as file:
                    lines = file.readlines()
                    if lines:
                        # Read the first two lines
                        first_line = lines[0].strip()
                        pid_line = lines[1].strip() if len(lines) > 1 else ""
                        
                        # Extract PID from the second line if present
                        pid = None
                        if "PID:" in pid_line:
                            try:
                                pid = int(pid_line.split('PID:')[1].strip())
                            except ValueError:
                                pid = None
                                
                        # Determine status based on the PID
                        if pid and is_pid_running(pid):
                            status = "running"
                        else:
                            # Check the last line for success or failure
                            last_line = lines[-1].strip()
                            if "SUCCESS:" in last_line:
                                status = "completed"
                            elif "FATAL ERROR:" in last_line:
                                status = "failed"
                            else:
                                status = "unknown"

            except (FileNotFoundError, PermissionError):
                status = "unknown"
            
            log_files.append({
                'filename': log_file,
                'status': status
            })

        return render_template('users/import/list_import_cp_logs.html', title='cPanel Imports', log_files=log_files)

    if request.method == 'POST':
        backup_path = request.form.get('path')
        plan_name = request.form.get('plan_name')

        # Validate inputs
        if not backup_path or not plan_name:
            return jsonify({
                'status': 'error',
                'message': 'Backup path and plan name are required.'
            })

        temp_dir = '/tmp/cPanel-to-OpenPanel/'
        repo_url = 'https://github.com/stefanpejcic/cPanel-to-OpenPanel'
        clone_command = ['git', 'clone', repo_url, temp_dir]
        import_script = os.path.join(temp_dir, 'cp-import.sh')
        import_command = ['bash', import_script, '--backup-location', backup_path, '--plan-name', plan_name]

        # Remove existing directory
        if os.path.exists(temp_dir):
            subprocess.run(['rm', '-rf', temp_dir], check=True)


        try:
            subprocess.run(clone_command, check=True)
            subprocess.Popen(import_command, stdout=subprocess.PIPE, stderr=subprocess.PIPE, text=True)

            return jsonify({
                'status': 'success',
                'message': 'Import process has started.'
            })

        except subprocess.CalledProcessError as e:
            return jsonify({
                'status': 'error',
                'message': f'Error during execution: {e.stderr}'
            })

        except Exception as e:
            return jsonify({
                'status': 'error',
                'message': str(e)
            })



@app.route('/import/cpanel/log/<path:log_filename>', methods=['GET'])
@admin_required_route
def view_cp_import_log(log_filename):
    try:
        log_dir = '/var/log/openpanel/admin/imports/'
        log_path = os.path.join(log_dir, log_filename)

        if not os.path.isfile(log_path):
            return jsonify({
                'status': 'error',
                'message': 'Log file does not exist.'
            }), 404
        
        with open(log_path, 'r') as file:
            log_content = file.read()

        return jsonify({
            'status': 'success',
            'log': log_content
        })
    
    except OSError as e:
        return jsonify({
            'status': 'error',
            'message': f'Error accessing log file: {str(e)}'
        }), 500
    
    except Exception as e:
        return jsonify({
            'status': 'error',
            'message': str(e)
        }), 500









import glob
# https://github.com/stefanpejcic/OpenPanel/issues/239
@app.route('/json/cpanel-backups', methods=['GET'])
@admin_required_route
def list_cpanel_backups():
    search_dirs = ['/', '/home', '/root']
    pattern = 'backup-*.tar.gz'
    backups = []
    for directory in search_dirs:
        full_pattern = os.path.join(directory, pattern)
        found_files = glob.glob(full_pattern)
        backups.extend(found_files)

    return jsonify(backups)







# OPENPANEL 2 OPENPANEL IMPORT


def is_domain(server):
    # Simple regex to check if the input is a domain
    domain_regex = re.compile(
        r'^(?:[a-zA-Z0-9-]+\.)+[a-zA-Z]{2,}$'
    )
    return domain_regex.match(server) is not None

def configure_iptables():
    try:
        # Allow outgoing connections on port 2087
        subprocess.run(['sudo', 'iptables', '-A', 'OUTPUT', '-p', 'tcp', '--dport', '2087', '-j', 'ACCEPT'], check=True)
        return True
    except subprocess.CalledProcessError as e:
        print(f"Error configuring iptables: {e}")
        return False




@app.route('/import/users/', methods=['GET', 'POST', 'PUT'])
@admin_required_route
def import_users():
    if request.method == 'GET':
        return render_template('users/import/import_users.html')



    elif request.method == 'POST':
        try:
            server = request.form['server']
            username = request.form['username']
            password = request.form['password']

            if not configure_iptables():
                return jsonify({"status": "error", "message": "Failed to configure iptables"}), 500

            protocol = 'https' if is_domain(server) else 'http'
            base_api_endpoint = f'{protocol}://{server}:2087/api/'

            get_response = requests.get(base_api_endpoint, timeout=10)
            get_response.raise_for_status()

            if get_response.status_code == 200 and get_response.json().get('message') == 'API is working!':
                auth_data = {'username': username, 'password': password}

                post_response = requests.post(base_api_endpoint, json=auth_data, headers={"Content-Type": "application/json"}, timeout=10)
                post_response.raise_for_status()

                if post_response.status_code == 200:
                    token = post_response.json().get('access_token')
                    if token:
                        users_endpoint = f'{base_api_endpoint}users'
                        headers = {"Authorization": f"Bearer {token}"}
                        users_response = requests.get(users_endpoint, headers=headers, timeout=10)
                        users_response.raise_for_status()

                        if users_response.status_code == 200:
                            users_data = users_response.json().get('users', [])
                            # Convert to a list of dicts for easier templating
                            formatted_users_data = [
                                {
                                    'id': user[0],
                                    'username': user[1],
                                    'password': user[2],
                                    'email': user[3],
                                    'owner': user[4],  # deprecated!
                                    'user_domains': user[5],
                                    'twofa_enabled': user[6],
                                    'otp_secret': user[7],
                                    'plan': user[8],
                                    'registered_date': user[9],
                                    'plan_id': user[10],
                                    'plan_name': user[11]
                                }
                                for user in users_data
                            ]
                            session['users_data'] = formatted_users_data
                            return render_template('users/import/display_users.html', title='Import OpenPanel users', users=formatted_users_data)
                        else:
                            return jsonify({"status": "error", "message": users_response.text}), users_response.status_code
                    else:
                        return jsonify({"status": "error", "message": "Token not found in response"}), 400
                else:
                    return jsonify({"status": "error", "message": post_response.text}), post_response.status_code
            else:
                return jsonify({"status": "error", "message": "API check failed or incorrect message"}), get_response.status_code
        except Exception as e:
            return jsonify({"status": "error", "message": str(e)}), 400

    elif request.method == 'PUT':
        import_data = session.get('users_data', [])
        progress = 0
        total = len(import_data)
        while progress < total:
            # Simulate some work being done
            progress += 1
            # todo
        return jsonify({"status": "success", "message": "Import completed", "progress": progress, "total": total}), 200
    
