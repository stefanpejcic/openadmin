################################################################################
# Author: Stefan Pejcic
# Created: 21.06.2024
# Last Modified: 21.06.2024
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
import subprocess
from flask import Flask, Response, abort, render_template, request, send_file, g, jsonify, session, url_for, flash, redirect, get_flashed_messages
from collections import deque 
from app import app, login_required_route


# Path to the configuration file
CONFIG_PATH = '/etc/openpanel/openadmin/config/log_paths.json'

# Fallback log paths if the JSON file is missing or invalid
FALLBACK_LOG_FILES = {
    "Nginx Access Log": "/var/log/nginx/access.log",
    "Nginx Error Log": "/var/log/nginx/access.log",
    "OpenAdmin Access Log": "/var/log/openpanel/admin/access.log",
    "OpenAdmin Error Log": "/var/log/openpanel/admin/error.log",
    "OpenAdmin API Log": "/var/log/openpanel/admin/api.log",
    "OpenAdmin Login Log": "/var/log/openpanel/admin/login.log",
    "OpenAdmin Notifications": "/var/log/openpanel/admin/notifications.log",
    "OpenAdmin Crons Log": "/var/log/openpanel/admin/cron.log",
    "OpenPanel Access Log": "/var/log/openpanel/user/access.log",
    "OpenPanel Error Log": "/var/log/openpanel/user/error.log",
    "Certbot SSL Logs": "/var/log/letsencrypt/letsencrypt.log",
    "MySQL Logs": "/var/log/mysql.log",
    "UFW Logs": "/var/log/ufw.log",
    "AuthLog": "/var/log/auth.log",
    "DPKG Log": "/var/log/dpkg.log",
    "Syslog": "/var/log/syslog"
}

def load_log_paths():
    if os.path.exists(CONFIG_PATH):
        try:
            with open(CONFIG_PATH, 'r') as config_file:
                return json.load(config_file)
        except json.JSONDecodeError:
            print(f"Error: Invalid JSON format in {CONFIG_PATH}. Using fallback.")
            return FALLBACK_LOG_FILES
    else:
        print(f"Warning: Config file {CONFIG_PATH} not found. Using fallback.")
        return FALLBACK_LOG_FILES


LOG_FILES = load_log_paths()


@app.route('/services/logs')
@login_required_route
def logs():
    log_files = load_log_paths()  # Load log paths fresh each time the route is accessed
    return render_template('services/logs.html', log_files=log_files)


@app.route('/services/logs/raw', methods=['GET', 'POST', 'DELETE'])
@login_required_route
def view_log():
    log_name = request.args.get('log_name')
    lines = request.args.get('lines')
    log_files = load_log_paths()
    log_path = log_files.get(log_name)
    
    if not log_path and log_name not in ["MySQL Logs", "OpenPanel Error Log"]:
        return jsonify({"error": "Log file not found"}), 404
    
    if request.method == 'GET':
        if log_name == "MySQL Logs":
            log_content = get_docker_log("openpanel_mysql")
        elif log_name == "OpenPanel Error Log":
            log_content = get_docker_log("openpanel")
        else:
            try:
                with open(log_path, 'r') as log_file:
                    if lines == 'ALL':
                        log_content = log_file.read()
                    else:
                        # Read only the last 'lines' number of lines
                        log_content = ''.join(deque(log_file, int(lines)))
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
        if log_name in ["MySQL Logs", "OpenPanel Error Log"]:
            return jsonify({"error": "Cannot delete log from Docker container"}), 403
        else:
            try:
                with open(log_path, 'w') as log_file:
                    log_file.truncate(0)
                return jsonify({"message": f"Log file {log_path} emptied."})
            except FileNotFoundError:
                return jsonify({"error": "Log file not found"}), 404
            except Exception as e:
                return jsonify({"error": str(e)}), 500

def get_docker_log(container_name):
    try:
        log_output = subprocess.check_output(['docker', 'logs', container_name], text=True)
        return log_output
    except subprocess.CalledProcessError as e:
        return f"Error retrieving logs from {container_name}: {str(e)}"
