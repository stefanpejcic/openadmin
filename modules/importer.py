# import python modules
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
import os
import re
import json
from flask import Flask, Response, render_template, request, g, jsonify, session, redirect, url_for, flash, get_flashed_messages
import subprocess
import datetime
from collections import OrderedDict
import psutil
from subprocess import run, Popen, PIPE
import requests
from requests.exceptions import RequestException, HTTPError, ConnectionError, Timeout
import glob
import signal

# import our modules
from app import app, admin_required_route, load_openpanel_config
from modules.helpers import get_all_users, get_user_and_plan_count, get_all_plans

# CPANEL 2 OPENPANEL IMPORT
def is_pid_running(pid):
    print(f"IMPORTER - Check if a process with the given PID is running: {pid}")        
    try:
        subprocess.check_output(['ps', '-p', str(pid)])
        return True
    except subprocess.CalledProcessError:
        return False





@app.route('/user/import', methods=['GET'])
@admin_required_route
def import_cp_user():
    current_route = request.path
    hosting_plans = get_all_plans()
    plans = hosting_plans if hosting_plans else []
    messages = get_flashed_messages()       
    return render_template('users/import/import.html', 
        title='Import User', 
        messages=messages, 
        plans=plans, 
        app=app, 
        current_route=current_route
        )











@app.route('/import/cpanel', methods=['GET', 'POST'])
@admin_required_route
def import_users_from_cpanel():

    if request.method == 'POST':
        backup_path = request.form.get('path')
        plan_name = request.form.get('plan_name')
        print(f"IMPORTER - Importing user account from cPanel backup: {backup_path} to plan: {plan_name}")

        # Validate inputs
        if not backup_path or not plan_name:
            print(f"IMPORTER - Aborting import: path or plan_name are not provided.")        
            flash('Backup path and plan name are required.', 'danger')
            return redirect(url_for('import_cp_user'))
            
        temp_dir = '/tmp/cPanel-to-OpenPanel/'
        repo_url = 'https://github.com/stefanpejcic/cPanel-to-OpenPanel'
        clone_command = ['git', 'clone', repo_url, temp_dir]
        import_script = os.path.join(temp_dir, 'cp-import.sh')
        import_command = ['bash', import_script, '--backup-location', backup_path, '--plan-name', plan_name]

        # Remove existing directory
        if os.path.exists(temp_dir):
            print(f"IMPORTER - Removing: {temp_dir}")
            subprocess.run(['rm', '-rf', temp_dir], check=True)

        try:
            print(f"IMPORTER - Executing: {clone_command}")
            subprocess.run(clone_command, check=True)
            subprocess.Popen(import_command, stdout=subprocess.PIPE, stderr=subprocess.PIPE, text=True)
            flash(f'Import process has started.', 'success')
        except subprocess.CalledProcessError as e:
            print(f"IMPORTER - Error executing command, error: {str(e)}")
            flash(f'Error during execution: {e.stderr}', 'warning')
            return redirect(url_for('import_cp_user'))
        except Exception as e:
            flash(str(e), 'danger')
            print(f"IMPORTER - Error executing command, executing: {str(e)}")
            return redirect(url_for('import_cp_user'))     
        return redirect(url_for('import_users_from_cpanel'))  

    # after post and on get
    log_dir = '/var/log/openpanel/admin/imports/'
    log_files = []
    statuses = {}
    now = datetime.datetime.now()
    print(f"IMPORTER - Checking statuses for imports in: {log_dir}")

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




@app.route('/import/user/log/<path:log_filename>', methods=['GET'])
@admin_required_route
def view_transfer_import_log(log_filename):
    try:
        log_dir = '/var/log/openpanel/admin/transfers/'
        log_path = os.path.join(log_dir, log_filename)

        if not os.path.isfile(log_path):
            print(f"IMPORTER - Log file: {log_path} does not exist.")
            if request.args.get('output') == 'json':
                return jsonify({
                    'status': 'error',
                    'message': 'Log file does not exist.'
                }), 404
            return Response("Log file does not exist.", status=404, mimetype='text/plain')

        print(f"IMPORTER - Reading log file: {log_path}")
        with open(log_path, 'r') as file:
            log_content = file.read()

        if request.args.get('output') == 'json':
            return jsonify({
                'status': 'success',
                'log': log_content
            })

        return Response(log_content, mimetype='text/plain')

    except OSError as e:
        print(f"IMPORTER - Error accessing log file: {str(e)")
        if request.args.get('output') == 'json':
            return jsonify({
                'status': 'error',
                'message': f'Error accessing log file: {str(e)}'
            }), 500
        return Response(f"Error accessing log file: {str(e)}", status=500, mimetype='text/plain')

    except Exception as e:
        print(f"IMPORTER - Exception reading log file: {str(e)")
        if request.args.get('output') == 'json':
            return jsonify({
                'status': 'error',
                'message': str(e)
            }), 500
        return Response(str(e), status=500, mimetype='text/plain')


@app.route('/import/cpanel/log/<path:log_filename>', methods=['GET'])
@admin_required_route
def view_cp_import_log(log_filename):
    try:
        log_dir = '/var/log/openpanel/admin/imports/'
        log_path = os.path.join(log_dir, log_filename)

        if not os.path.isfile(log_path):
            print(f"IMPORTER - Log does not exist: {log_path}")
            if request.args.get('output') == 'json':
                return jsonify({
                    'status': 'error',
                    'message': 'Log file does not exist.'
                }), 404
            return Response("Log file does not exist.", status=404, mimetype='text/plain')

        print(f"IMPORTER - Reading: {log_path}")
        with open(log_path, 'r') as file:
            log_content = file.read()

        if request.args.get('output') == 'json':
            return jsonify({
                'status': 'success',
                'log': log_content
            })

        return Response(log_content, mimetype='text/plain')

    except OSError as e:
        print(f"IMPORTER - Error accessing log file: {str(e)}")
        if request.args.get('output') == 'json':
            return jsonify({
                'status': 'error',
                'message': f'Error accessing log file: {str(e)}'
            }), 500
        return Response(f"Error accessing log file: {str(e)}", status=500, mimetype='text/plain')

    except Exception as e:
        print(f"IMPORTER - Exception accessing log file: {str(e)}")
        if request.args.get('output') == 'json':
            return jsonify({
                'status': 'error',
                'message': str(e)
            }), 500
        return Response(str(e), status=500, mimetype='text/plain')





# https://github.com/stefanpejcic/OpenPanel/issues/239
@app.route('/json/cpanel-backups', methods=['GET'])
@admin_required_route
def list_cpanel_backups():
    search_dirs = ['/', '/home', '/root']
    pattern = 'backup-*.tar.gz'
    print(f"IMPORTER - Checking for backup-*.tar.gz files in paths: / /home /root")
    backups = []
    for directory in search_dirs:
        full_pattern = os.path.join(directory, pattern)
        found_files = glob.glob(full_pattern)
        backups.extend(found_files)
    return jsonify(backups)


@app.route('/json/transfers', methods=['GET'])
@admin_required_route
def list_transfers():
    search_dir = '/var/log/openpanel/admin/transfers/'
    pattern = '*.log'
    full_pattern = os.path.join(search_dir, pattern)
    print(f"IMPORTER - Listing transfers: {full_pattern}")
    files = glob.glob(full_pattern)
    return jsonify(files)




@app.route('/json/transfers/<username>', methods=['GET'])
@admin_required_route
def list_transfers_for(username):
    if "SUSPENDED_" in username:
        username = username.rsplit("_", 1)[-1]

    search_dir = '/var/log/openpanel/admin/transfers/'
    pattern = f'{username}_*.log'
    full_pattern = os.path.join(search_dir, pattern)
    files = glob.glob(full_pattern)

    transfers = []
    print(f"IMPORTER - Listing transfer logs: {full_pattern}")

    for filepath in files:
        transfer = {
            'filename': os.path.basename(filepath),
            'file': filepath,
            'status': 'unknown',
            'pid': None
        }

        try:
            with open(filepath, 'r') as f:
                lines = f.readlines()

            pid_line = next((line for line in lines if 'PID:' in line), None)
            if pid_line:
                pid = int(pid_line.strip().split('PID:')[1])
                transfer['pid'] = pid

                try:
                    os.kill(pid, 0)
                    transfer['status'] = 'in progress'
                except ProcessLookupError:
                    if lines and 'SUCCESS:' in lines[-1]:
                        transfer['status'] = 'success'
                    else:
                        transfer['status'] = 'failed'
            else:
                transfer['status'] = 'failed'

        except Exception as e:
            transfer['error'] = str(e)
            transfer['status'] = 'error'

        transfers.append(transfer)

    return jsonify(transfers)


# OPENPANEL 2 OPENPANEL IMPORT


def is_domain(server):
    print(f"IMPORTER - Validating domain: {server}")
    domain_regex = re.compile(
        r'^(?:[a-zA-Z0-9-]+\.)+[a-zA-Z]{2,}$'
    )
    return domain_regex.match(server) is not None

def configure_iptables(server):
    print(f"IMPORTER - Executing: csf -ta {server} 3600")
    try:
        subprocess.run(['csf', '-ta', server, '3600'], check=True)
        return True
    except subprocess.CalledProcessError as e:
        print(f"IMPORTER - Error temporary iptables: {e}")
        return False


def mask_secret(s: str, show=2, mask_char='*'):
    if not s:
        return None
    visible = s[-show:] if len(s) > show else s
    masked_len = max(1, len(s) - len(visible))
    return mask_char * masked_len + visible

@app.route('/import/transfer/', methods=['GET', 'POST'])
@admin_required_route
def import_users():
    if request.method == 'GET':
        log_dir = '/var/log/openpanel/admin/transfers/'
        log_files = []
        statuses = {}
        now = datetime.datetime.now()

        print(f"IMPORTER - Checking for .log files in: {log_dir}")
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

        return render_template('users/import/import_users.html', title='Account Transfer Logs', log_files=log_files)


    elif request.method == 'POST':
        openpanel_username = request.form.get('openpanel_username')
        server = request.form.get('server')
        username = request.form.get('username', 'root')
        password = request.form.get('password')
        port = request.form.get('port')
        live_transfer = request.form.get('live_transfer')

        print(f"IMPORTER - Received transfer request for: username: {openpanel_username} | "
            f"live_transfer: {live_transfer} | server: {server} | ssh_port: {port} | "
            f"ssh_username: {username} | ssh_password: {mask_secret(password, show=2)}")
        if not openpanel_username or not server:
            print(f"IMPORTER - Aborting transfer: server IP or username are not provided.")
            flash(f"Error: Server IP and OpenPanel username are required.", 'danger')
            return redirect(f'/users/{openpanel_username}#transfer')

        try:
            local_command = [
                "opencli",
                "user-transfer",
                "--account", openpanel_username,
                "--host", server,
                "--username", username
            ]

            if password:
                local_command += ["--password", password]

            if port:
                local_command += ["--port", str(port)]

            if live_transfer:
                local_command.append("--live-transfer")

            if not is_domain(server):
                configure_iptables(server)

            print(f"IMPORTER - Executing: {local_command}")
            subprocess.Popen(
                local_command,
                stdout=subprocess.DEVNULL,
                stderr=subprocess.DEVNULL,
                stdin=subprocess.DEVNULL,
                start_new_session=True
            )

            flash(f"Transfer process for account {openpanel_username} started in the background.", "success")

        except Exception as e:
            msg = f"Error starting transfer: {e}"
            print(f"IMPORTER - {msg}")
            flash(msg, "danger")

        return redirect(f'/users/{openpanel_username}#transfer')
