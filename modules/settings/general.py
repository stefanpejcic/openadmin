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
# Last Modified: 09.03.2024
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
import re
import json
import socket
from flask import Flask, Response, abort, render_template, request, send_file, g, jsonify, session, url_for, flash, redirect, get_flashed_messages
import subprocess
import datetime
import psutil
from app import app, cache, is_license_valid, admin_required_route, load_openpanel_config, connect_to_database, get_openpanel_port, get_openpanel_proxy, get_openpanel_domain, get_ip_address
import docker

@app.route('/settings/updates/update_now', methods=['POST'])
@admin_required_route
def update_now():
    try:
        # Start the update process in the background
        subprocess.Popen(['timeout', '600s', 'opencli', 'update', '--force'], start_new_session=True)
        flash('Update process started successfully.', 'info')
    except Exception as e:
        flash('Error: Failed to start the update process.", "details": str(e)}', 'error')
    return redirect(url_for('up_update_settings'))



@app.route('/settings/general', methods=['GET', 'POST'])
@admin_required_route
@cache.memoize(timeout=30)
def admin_general_settings():

    if request.method == 'POST':
        force_domain = request.form.get('force_domain')

        if force_domain:
            domain_name_is_set = True
            ip_address_is_set = False
        else:
            ip_address_is_set = True
            domain_name_is_set = False

        # todo: validate domain!

        admin_port_value = request.form.get('2087_port')
        openpanel_port_value = request.form.get('2083_port')
        openpanel_proxy = request.form.get('openpanel_proxy')

        admin_port = int(admin_port_value) if admin_port_value else 2087
        openpanel_port = int(openpanel_port_value) if openpanel_port_value else 2083

        force_domain_current_value = get_openpanel_domain()
        if force_domain_current_value != None:
            ip_pattern = r"^(\d{1,3}\.){3}\d{1,3}$"
            if re.match(ip_pattern, force_domain_current_value):
                force_domain_current_value = None
            else:
                force_domain_current_value = force_domain_current_value

        openpanel_port_current_value = get_openpanel_port()

        if int(openpanel_port) != int(openpanel_port_current_value):
            command = f"opencli port set '{openpanel_port}' --no-restart"
            result = subprocess.run(command, shell=True, text=True)          

        if openpanel_proxy:
            command = ["opencli", "proxy", "set", openpanel_proxy, "--no-restart"]
        else:
            command = ["opencli", "proxy", "set", 'openpanel', "--no-restart"]
        subprocess.run(command, text=True)

        if domain_name_is_set:
            if force_domain and force_domain_current_value != force_domain:
                command = f"opencli domain set {force_domain} --no-restart"
                result = subprocess.run(command, shell=True, text=True)
            elif force_domain and force_domain_current_value == force_domain:
                pass

        elif ip_address_is_set and force_domain_current_value:
            command = "opencli domain ip"
            result = subprocess.run(command, shell=True, text=True)


        # restart services only when needed!
        file_path = '/root/openpanel_restart_needed'
        with open(file_path, 'w') as f:
            f.write("Restart needed") 

        file_path = '/root/openadmin_restart_needed'
        with open(file_path, 'w') as f:
            f.write("Restart needed") 

    current_route = request.path
    server_hostname = socket.gethostname() or  subprocess.check_output(["hostname"]).decode("utf-8").strip()
    #legacy, will be repalced with openadmin > tempaltes
    howto_custom_content_for_users = '/etc/openpanel/openpanel/conf/knowledge_base_articles.json'
    try:
        with open(howto_custom_content_for_users, 'r') as file:
            howto_content_current_value = file.read()
            # Check if the file is empty
            if not howto_content_current_value:
                howto_content_current_value = None
    except FileNotFoundError:
        howto_content_current_value = "File not found"
    except Exception as e:
        howto_content_current_value = f"Error: {str(e)}"


    # invalidate caches so we display different on page reload!
    cache.delete_memoized(get_openpanel_proxy)
    cache.delete_memoized(get_openpanel_domain)
    cache.delete_memoized(get_openpanel_port)
    cache.delete_memoized(get_ip_address)

    port = get_openpanel_port()
    proxy = get_openpanel_proxy()
    force_domain = get_openpanel_domain()
    public_ip = get_ip_address()

    return render_template('settings/general.html', title='General Settings', current_route=current_route, app=app, server_hostname=server_hostname, port=port, proxy=proxy, howto_content_current_value=howto_content_current_value, force_domain=force_domain, public_ip=public_ip)







@cache.memoize(timeout=360)
def get_op_update_logs():
    log_dir = "/var/log/openpanel/updates/"
    if not os.path.exists(log_dir):
        updates = []
    else:
        updates = [
            {"file": filename, 
             "log_dir": log_dir,
             "timestamp": os.path.getmtime(os.path.join(log_dir, filename)),
             "human_readable_timestamp": datetime.datetime.fromtimestamp(os.path.getmtime(os.path.join(log_dir, filename))).strftime('%Y-%m-%d %H:%M:%S')}
            for filename in os.listdir(log_dir)
            if os.path.isfile(os.path.join(log_dir, filename))
        ]
        
    updates.sort(key=lambda x: x["timestamp"], reverse=True)
    return updates


import requests

@cache.memoize(timeout=7200)
def get_latest_version():
    image_name = 'openpanel/openpanel-ui' # todo: edit to check .env file!
    url = f"https://hub.docker.com/v2/repositories/{image_name}/tags"
    try:
        response = requests.get(url)
        response.raise_for_status()
        tags = response.json().get('results', [])
        
        versions = [
            tag['name'] for tag in tags 
            if tag['name'] != 'latest' and tag['name'].replace('.', '').isdigit()
        ]
        
        if not versions:
            return None
        
        return sorted(versions, key=lambda v: list(map(int, v.split('.'))))[-1]
    
    except requests.RequestException as e:
        print(f"Error fetching tags: {e}")
        return None


# UPDATE LOGS
import glob
def load_update_log_paths():
    dir = '/var/log/openpanel/updates'
    if os.path.exists(dir):
        log_files = glob.glob(os.path.join(dir, '*.log'))
        return log_files
    return []

@app.route('/settings/updates/log/', methods=['GET', 'POST'])
@admin_required_route
def op_update_logs_settings():
    current_route = '/services/logs'
    log_files = load_update_log_paths()
    return render_template('services/update_logs.html', current_route=current_route, log_files=log_files, title="Update Logs")

@app.route('/services/updates/log/raw', methods=['GET', 'POST', 'DELETE'])
@admin_required_route
def view_update_log():
    log_name = request.args.get('log_name')
    log_path = f'/var/log/openpanel/updates/{log_name}'
    if not log_path:
        return jsonify({"error": "Log file not found"}), 404

    if request.method == 'GET':
        try:
            with open(log_path, 'r') as log_file:
                log_content = log_file.read()
        except FileNotFoundError:
            return jsonify({"error": "Log file not found"}), 404
        except Exception as e:
            return jsonify({"error": str(e)}), 500
        return jsonify({"content": log_content})

    elif request.method == 'POST':
        if log_path:
            try:
                return send_file(log_path, as_attachment=True)
            except FileNotFoundError:
                return jsonify({"error": "Log file not found"}), 404
            except Exception as e:
                return jsonify({"error": str(e)}), 500
        else:
            return jsonify({"error": "Log file not found"}), 404
    
    elif request.method == 'DELETE':
        if os.path.exists(log_path):
            os.remove(log_path)
            return jsonify({"message": f"Log file {log_path} emptied."})
        else:
            return jsonify({"error": "Log file not found"}), 404







# CRASH LOGS
def load_crash_log_paths():
    dir = '/var/log/openpanel/admin/crashlog'
    if os.path.exists(dir):
        log_files = glob.glob(os.path.join(dir, '*.txt'))
        return log_files
    return []

@app.route('/services/crashlogs/log/', methods=['GET', 'POST'])
@admin_required_route
def op_crashlogs_logs_settings():
    current_route = '/services/logs'
    log_files = load_crash_log_paths()
    return render_template('services/crash_logs.html', current_route=current_route, log_files=log_files, title="Crashlogs")

@app.route('/services/crashlogs/log/raw', methods=['GET', 'POST', 'DELETE'])
@admin_required_route
def view_crashlogs_log():
    log_name = request.args.get('log_name')
    log_path = f'/var/log/openpanel/admin/crashlog/{log_name}'
    if not log_path:
        return jsonify({"error": "Log file not found"}), 404

    if request.method == 'GET':
        try:
            with open(log_path, 'r') as log_file:
                log_content = log_file.read()
        except FileNotFoundError:
            return jsonify({"error": "Log file not found"}), 404
        except Exception as e:
            return jsonify({"error": str(e)}), 500
        return jsonify({"content": log_content})

    elif request.method == 'POST':
        if log_path:
            try:
                return send_file(log_path, as_attachment=True)
            except FileNotFoundError:
                return jsonify({"error": "Log file not found"}), 404
            except Exception as e:
                return jsonify({"error": str(e)}), 500
        else:
            return jsonify({"error": "Log file not found"}), 404
    
    elif request.method == 'DELETE':
        if os.path.exists(log_path):
            os.remove(log_path)
            return jsonify({"message": f"Log file {log_path} emptied."})
        else:
            return jsonify({"error": "Log file not found"}), 404









@app.route('/settings/updates', methods=['GET', 'POST'])
@admin_required_route
def up_update_settings():

    config_file = '/etc/openpanel/openpanel/conf/openpanel.config'

    if request.method == 'POST':
        preference = request.form.get('preference')

        updates = {
            'minor_and_major': {'autoupdate': 'on', 'autopatch': 'on'},
            'minor_only': {'autoupdate': 'off', 'autopatch': 'on'},
            'major_only': {'autoupdate': 'on', 'autopatch': 'off'},
            'none': {'autoupdate': 'off', 'autopatch': 'off'}
        }.get(preference)

        with open(config_file, "r") as file:
            content = file.read()

        for key, value in updates.items():
            content = content.replace(f'{key}=on', f'{key}={value}').replace(f'{key}=off', f'{key}={value}')

        with open(config_file, "w") as file:
            file.write(content)

        with open('/root/openpanel_restart_needed', 'w') as f:
            f.write("Restart needed")

        cache.delete_memoized(load_openpanel_config)


    # read after update
    config_data = load_openpanel_config(config_file)
    current_route = request.path
    latest_version = get_latest_version()
    update_logs = get_op_update_logs()
    
    return render_template('settings/updates.html', title='Update Settings', current_route=current_route, app=app, config_data=config_data, latest_version=latest_version, update_logs=update_logs)
