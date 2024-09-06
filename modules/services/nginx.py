################################################################################
# Author: Stefan Pejcic
# Created: 22.06.2024
# Last Modified: 22.06.2024
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
import requests
from pathlib import Path
from flask import Flask, Response, abort, render_template, request, g, jsonify, session, url_for, flash, redirect, get_flashed_messages
from app import app, login_required_route

def get_nginx_info():
    # Get Nginx version
    version_result = subprocess.run(['docker', 'exec', 'nginx', 'nginx', '-v'], stderr=subprocess.PIPE, stdout=subprocess.PIPE)
    error_message = version_result.stderr.decode('utf-8').strip()

    if "No such container" in error_message: #not started yet
        version = None
    else:
        if error_message.startswith("nginx version: "):
            version = error_message[len("nginx version: "):]
        else:
            #print(f"An unexpected error occurred: {error_message}")
            version = "Unknown"


    # Get Nginx status
    status_result = subprocess.run(['docker', 'ps', '-q', '-f', 'name=nginx'], stdout=subprocess.PIPE)
    container_id = status_result.stdout.decode('utf-8').strip()
    if container_id:
        status = 'active'
    else:
        status = 'inactive'

    # Get active modules (using a hypothetical command)
    modules_result = subprocess.run(['docker', 'exec', 'nginx', 'nginx', '-V'], stderr=subprocess.PIPE, stdout=subprocess.PIPE)
    modules = [line.strip() for line in modules_result.stderr.decode('utf-8').split('--') if 'module' in line]

    # Get number of virtual host files
    vhosts_path = '/etc/nginx/sites-enabled'
    if os.path.exists(vhosts_path) and os.path.isdir(vhosts_path):
        vhosts_count = len([f for f in os.listdir(vhosts_path) if os.path.isfile(os.path.join(vhosts_path, f))])
    else:
        vhosts_count = 0

    return {
        'version': version,
        'status': status,
        'modules': modules,
        'vhosts_count': vhosts_count
    }

# Define the base directory for nginx configurations
nginx_base_dir = '/etc/openpanel/nginx/'

@app.route('/services/nginx/info', methods=['GET'])
@login_required_route
def nginx_info():
    info = get_nginx_info()
    return jsonify(info)



@app.route('/services/nginx/control', methods=['POST'])
@login_required_route
def nginx_control():
    action = request.json.get('action')
    if action not in ['start', 'stop', 'restart', 'reload']:
        abort(400, description="Invalid action")
    
    try:
        if action == 'reload':
            result = subprocess.run(
                ['docker', 'exec', 'nginx', 'nginx', '-s', 'reload'],
                stdout=subprocess.PIPE, stderr=subprocess.PIPE, check=True
            )
        elif action == 'start':
            result = subprocess.run(
                ['docker', 'compose', 'up', '-d', 'nginx'],
                cwd='/root',
                stdout=subprocess.PIPE, stderr=subprocess.PIPE, check=True
            )
        elif action == 'stop':
            result = subprocess.run(
                ['docker', 'compose', 'down'],
                cwd='/root',
                stdout=subprocess.PIPE, stderr=subprocess.PIPE, check=True
            )
        elif action == 'restart':
            result = subprocess.run(
                ['docker', 'compose', 'restart', 'nginx'],
                cwd='/root',
                stdout=subprocess.PIPE, stderr=subprocess.PIPE, check=True
            )
        
        return jsonify({"message": f"Nginx {action}ed successfully"})
    
    except subprocess.CalledProcessError as e:
        return jsonify({"error": e.stderr.decode('utf-8')}), 500


@app.route('/services/nginx/validate', methods=['GET'])
@login_required_route
def nginx_validate():
    # Check if the container is running
    status_check = subprocess.run(['docker', 'inspect', '--format={{.State.Running}}', 'nginx'], stdout=subprocess.PIPE, stderr=subprocess.PIPE)
    
    if status_check.stdout.decode('utf-8').strip() != 'true':
        return jsonify({"error": "Nginx container is not running"}), 500

    result = subprocess.run(['docker', 'exec', 'nginx', 'nginx', '-t'], stdout=subprocess.PIPE, stderr=subprocess.PIPE)
    
    if result.returncode != 0:
        return jsonify({"error": result.stderr.decode('utf-8')}), 500
    
    return jsonify({"message": "Configuration is valid"})


@app.route('/services/nginx/conf', methods=['GET', 'POST'])
@login_required_route
def nginx_conf():
    file_paths_allowed = [
        nginx_base_dir + 'nginx.conf',
        nginx_base_dir + 'vhosts/default.conf',
        nginx_base_dir + 'vhosts/domain.conf',
        nginx_base_dir + 'vhosts/domain.conf_with_modsec',
        nginx_base_dir + 'vhosts/docker_nginx_domain.conf',
        nginx_base_dir + 'vhosts/docker_apache_domain.conf',
        nginx_base_dir + 'vhosts/openpanel_proxy.conf'
    ]

    template_files = [
        nginx_base_dir + 'vhosts/domain.conf',
        nginx_base_dir + 'vhosts/domain.conf_with_modsec',
        nginx_base_dir + 'vhosts/docker_nginx_domain.conf',
        nginx_base_dir + 'vhosts/docker_apache_domain.conf'
    ]

    if request.method == 'POST':
        file_path = None
        backup_path = None
        try:
            file_path = request.args.get('file_path')
            if file_path is None or file_path not in file_paths_allowed:
                abort(403, description="Forbidden")

            new_config = request.json.get('config')
            if not new_config:
                abort(400, description="No configuration provided")

            # Allow editing of .conf and .html files in the error_pages directory and subdirectories
            if file_path.startswith(nginx_base_dir + 'error_pages/'):
                if not file_path.endswith('.conf') and not file_path.endswith('.html'):
                    abort(403, description="Forbidden")

            # Backup current config
            backup_path = file_path + '.bak'
            os.rename(file_path, backup_path)

            # Write new configuration
            with open(file_path, 'w') as file:
                file.write(new_config)

            # Validate the new config if it's not a template file
            if file_path not in template_files:
                validation_command = ['docker', 'exec', 'nginx', 'nginx', '-t']
                result = subprocess.run(validation_command, stdout=subprocess.PIPE, stderr=subprocess.PIPE)
                if result.returncode != 0:
                    os.rename(backup_path, file_path)
                    return jsonify({"error": result.stderr.decode('utf-8')}), 400

            # Reload nginx to apply the new configuration
            subprocess.run(['docker', 'exec', 'nginx', 'nginx', '-s', 'reload'])
            return jsonify({"message": "Configuration updated successfully"})

        except Exception as e:
            if backup_path and os.path.exists(backup_path):
                os.rename(backup_path, file_path)
            return jsonify({"error": str(e)}), 500

    elif request.method == 'GET':
        try:
            file_path = request.args.get('file_path')
            if file_path is None or file_path not in file_paths_allowed:
                abort(403, description="Forbidden")

            with open(file_path, 'r') as file:
                config = file.read()

            return jsonify({"config": config})

        except Exception as e:
            return jsonify({"error": str(e)}), 500


@app.route('/services/nginx/status', methods=['GET'])
@login_required_route
def nginx_status():
    status_url = 'http://127.0.0.1/nginx_status'
    response = requests.get(status_url)
    
    if response.status_code != 200:
        return jsonify({"error": "Unable to fetch Nginx status"}), 500
    
    status_lines = response.text.split('\n')
    status_info = {
        "active_connections": status_lines[0].split(': ')[1],
        "accepts_handled_requests": status_lines[2].split(),
        "reading": status_lines[3].split()[1],
        "writing": status_lines[3].split()[3],
        "waiting": status_lines[3].split()[5]
    }
    
    return jsonify(status_info)


@app.route('/services/nginx', methods=['GET'])
@login_required_route
def nginx_service():
    return render_template('services/nginx.html')
