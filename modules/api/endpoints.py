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
# Last Modified: 01.04.2024
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




# Python modules
import logging
from logging.handlers import RotatingFileHandler
import os
from flask import Flask, Response, render_template, request, g, jsonify, session, redirect, url_for, flash, Blueprint
import subprocess
from flask_jwt_extended import JWTManager, create_access_token, jwt_required, get_jwt_identity
import requests
import json
import docker
import platform
import psutil
import distro
import time



# Our modules
from app import app, csrf, db, load_openpanel_config, get_openpanel_version, get_openpanel_port, get_openpanel_domain
from modules.login import User
from modules.helpers import get_all_users, get_user_and_plan_count, get_plan_by_id, get_all_plans, get_userdata_by_username, get_hosting_plan_name_by_id, get_user_websites, is_username_unique, gravatar_url
from modules.general.errors import api_logger

# all endpoints should have /api prefix
api = Blueprint('api', __name__, url_prefix='/api')

user_config_file_path = '/etc/openpanel/openpanel/conf/openpanel.config'
config_data = load_openpanel_config(user_config_file_path)
dev_mode = config_data.get('PANEL', {}).get('dev_mode', 'off') # off for versions <0.1.6

# LOG API REQUESTS
if dev_mode.lower() == 'off':
    # logging
    @api.before_request
    def log_api_request_info():
        api_logger.info(f"Received {request.method} request for {request.url} from {request.remote_addr} - for detailed output enable dev_mode")

    @api.after_request
    def log_api_response_info(response):
        api_logger.info(f"Responded with {response.status_code} to {request.method} request for {request.url} from {request.remote_addr}")
        return response
else:
    @api.before_request
    def log_api_request_info():
        try:
            if request.is_json:
                request_data = request.get_json()
            else:
                request_data = request.data  # Fallback for non-JSON data

            api_logger.info(
                f"Received {request.method} request for {request.url} "
                f"from {request.remote_addr} with data: {request_data}"
            )
        except Exception as e:
            api_logger.error(f"Error logging request data: {e}")

    @api.after_request
    def log_api_response_info(response):
        try:
            if response.is_json:
                response_data = response.get_json()
            else:
                response_data = response.data  # Fallback for non-JSON data

            api_logger.info(
                f"Responded with {response.status_code} to {request.method} request "
                f"for {request.url} from {request.remote_addr} with response data: {response_data}"
            )
        except Exception as e:
            api_logger.error(f"Error logging response data: {e}")

        return response


def load_allowed_services():
    fileconfig_path = '/etc/openpanel/openadmin/config/services.json'
    try:
        with open(fileconfig_path, 'r') as file:
            services = json.load(file)
    except (IOError, json.JSONDecodeError) as e:
        # Handle file read errors or JSON parse errors
        print(f"Error reading or parsing the file: {e}")
        return []

    # Extract 'real_name' of services that are on the dashboard
    allowed_services = [service['real_name'] for service in services if service.get('on_dashboard', False)]
    return allowed_services






########### api enpoints start here



# ENDPOINT: /api/
# DESCRIPTION: On GET returns api status, on POST returns access token
# TYPE: GET POST
# EXAMPLES: 
#
# curl -X GET http://localhost:2087/api/
#
# curl -X POST http://localhost:2087/api/ -H "Content-Type: application/json" -d '{"username":"admin", "password":"kQsUFhwkzBCw3M57"}'
@api.route('/', methods=['GET', 'POST'])
@csrf.exempt
def welcome():
    if request.method == 'GET':
        return jsonify(message="API is working!"), 200

    elif request.method == 'POST':
        data = request.get_json()
        username = data.get('username')
        password = data.get('password')

        user = User.query.filter_by(username=username).first()

        if user and user.check_password(password):
            access_token = create_access_token(identity=username)
            return jsonify(access_token=access_token), 200
        else:
            return jsonify({"error": "Invalid credentials"}), 401



# ENDPOINT: /api/whoami
# DESCRIPTION: protected route that can be accessed only with a token and returns a username
# TYPE: GET
# EXAMPLE: curl -X GET http://localhost:2087/api/whoami -H "Authorization: Bearer TOKEN_HERE"
#
@api.route('/whoami', methods=['GET'])
@csrf.exempt
@jwt_required()
def protected():
    current_user = get_jwt_identity()
    return jsonify(logged_in_as=current_user), 200



import random
import re
import string
def generate_random_token(length):
    characters = string.ascii_letters + string.digits
    return ''.join(random.choice(characters) for _ in range(length))

def read_config_file(file_path): # read from /etc/openpanel/openpanel/conf/openpanel.config
    config = {}
    try:
        with open(file_path, 'r') as file:
            lines = file.readlines()
            for line in lines:
                match = re.match(r'(\w+)=(.*)', line)
                if match:
                    key, value = match.groups()
                    config[key] = value.strip()
    except FileNotFoundError:
        pass
    return config





# ENDPOINT: /api/users
# TYPE: GET POST DELETE PATCH CONNECT PUT
# DESCRIPTION: Manage users
# EXAMPLES:
# curl -X GET http://localhost:2087/api/users -H "Authorization: Bearer eyJ0eXAiOiJKV1QiLCJhbGciOiJIUzI1NiJ9.eyJmcmVzaCI6ZmFsc2UsImlhdCI6MTcxMDMzOTE5OCwianRpIjoiOGIxZmRmNWUtOTdhNy00YmU4LThkN2ItOWRjNmQ4ZjZhNTQzIiwidHlwZSI6ImFjY2VzcyIsInN1YiI6ImFkbWluIiwibmJmIjoxNzEwMzM5MTk4LCJjc3JmIjoiNDFlZGVhZWQtYWY1MC00MmY2LWFiYWMtYzM1M2Q2MGQyNzY1IiwiZXhwIjoxNzEwMzQwMDk4fQ.AYtC9GPqj3bEPuStc9f2CttBYh8I5rsRsB-gaQ1-xyg"
#
# curl -X POST http://localhost/api/users \ -H "Content-Type: application/json" \ -H "Authorization: Bearer TOKEN_HERE" \ -d '{     "email": "stefan@pejcic.rs",     "username": "stefansss",     "password": "stefan",     "plan_name": "default_plan_apache" }'
#
@api.route('/users', methods=['GET', 'POST'])
@api.route('/users/<username>', methods=['GET', 'POST', 'DELETE', 'PATCH', 'CONNECT', 'PUT'])
@csrf.exempt
@jwt_required()
def api_get_users(username=None):
    if request.method == 'GET':
        if username is not None:
            user = get_userdata_by_username(username)
            if user is None:
                return jsonify({"error": "User not found"}), 404
            return jsonify({"user": user})
        else:
            users = get_all_users()
            return jsonify({"users": users})


    elif request.method == "PUT":
        if not request.is_json:
            return jsonify({"error": "Invalid JSON format"}), 400
        
        plan_name = request.json.get('plan_name')

        if username is None or plan_name is None:
            return jsonify({"error": "Missing username or plan name."}), 400
        else:
            change_plan_command = f"opencli user-change_plan {username} {plan_name}"
            try:
                response = subprocess.check_output(change_plan_command, shell=True, text=True)
                formatted_response = {'message': response.strip()}
                return jsonify({'success': True, 'response': formatted_response}), 201
            except subprocess.CalledProcessError as e:
                error_message = f"Error changing plan for user: {e}"
                return jsonify({'success': False, 'error': error_message}), 500

    elif request.method == "CONNECT":
        if not request.is_json:
            return jsonify({"error": "Invalid JSON format"}), 400

        if username is None:
            return jsonify({"error": "Missing username"}), 400

        token_dir = f'/etc/openpanel/openpanel/core/users/{username}/'
        token_path = f'{token_dir}logintoken.txt'

        if not os.path.exists(token_dir):
            os.makedirs(token_dir)

        random_token = generate_random_token(30)
        with open(token_path, 'w') as file:
            file.write(random_token)

        config_file_path = '/etc/openpanel/openpanel/conf/openpanel.config'
        config = read_config_file(config_file_path)
        port = get_openpanel_port()

        force_domain_value = get_openpanel_domain()

        if force_domain:
            hostname = force_domain.strip()
        else:
            output = subprocess.check_output(["hostname", "-I"]).decode("utf-8").strip()
            ips = output.split()
            hostname = ips[0] if ips else "localhost"

        open_panel = f'{scheme}://{hostname}:{port}'

        open_panel_login_autologin = f'{open_panel}/login_autologin?username={username}&admin_token={random_token}'

        response_data = {
            'link': open_panel_login_autologin
        }

        return jsonify(response_data)




    elif request.method == 'PATCH':
        if not request.is_json:
            return jsonify({"error": "Invalid JSON format"}), 400
        password = request.json.get('password')
        action = request.json.get('action')

        if username is None:
            return jsonify({"error": "Missing username"}), 400
   
        elif username is not None and password:   
            command = f"opencli user-password {username} {password} --ssh"
            try:
                response = subprocess.check_output(command, shell=True, text=True)
                formatted_response = {'message': response.strip()}
                return jsonify({'success': True, 'response': formatted_response}), 201
            except subprocess.CalledProcessError as e:
                error_message = f"Error executing command: {e}"
                return jsonify({'success': False, 'error': error_message}), 500

        elif username is not None and action is not None:   
            if action not in ['suspend', 'unsuspend']:
                return jsonify({"error": "Invalid action, only suspend and unsuspend are allowed."}), 400
            if action == 'suspend':
                suspend_command = f"opencli user-suspend {username}"
                try:
                    response = subprocess.check_output(suspend_command, shell=True, text=True)
                    formatted_response = {'message': response.strip()}
                    return jsonify({'success': True, 'response': formatted_response}), 201
                except subprocess.CalledProcessError as e:
                    error_message = f"Error suspending user: {e}"
                    return jsonify({'success': False, 'error': error_message}), 500
            elif action == 'unsuspend':
                unsuspend_command = f"opencli user-unsuspend {username}"
                try:
                    response = subprocess.check_output(unsuspend_command, shell=True, text=True)
                    formatted_response = {'message': response.strip()}
                    return jsonify({'success': True, 'response': formatted_response}), 201
                except subprocess.CalledProcessError as e:
                    error_message = f"Error unsuspending user: {e}"
                    return jsonify({'success': False, 'error': error_message}), 500
        else:
            return jsonify({"error": "Something went wrong.."}), 400




    elif request.method == 'DELETE':
        if not request.is_json:
            return jsonify({"error": "Invalid JSON format"}), 400
        if username is not None:   
            command = f"opencli user-delete {username} -y"
            try:
                response = subprocess.check_output(command, shell=True, text=True)
                formatted_response = {'message': response.strip()}
                return jsonify({'success': True, 'response': formatted_response}), 201
            except subprocess.CalledProcessError as e:
                error_message = f"Error executing command: {e}"
                return jsonify({'success': False, 'error': error_message}), 500
        else:
            return jsonify({"error": "Missing username"}), 400




    elif request.method == 'POST':
        if not request.is_json:
            return jsonify({"error": "Invalid JSON format"}), 400

        email = request.json.get('email')
        username = request.json.get('username')
        password = request.json.get('password')
        plan_name = request.json.get('plan_name')

        if not email or not username or not password or not plan_name:
            return jsonify({"error": "Missing required fields"}), 400

        forbidden_usernames_file = "/etc/openpanel/openadmin/config/forbidden_usernames.txt"
        if os.path.exists(forbidden_usernames_file):
            with open(forbidden_usernames_file, "r") as file:
                forbidden_usernames = [line.strip() for line in file.readlines()]
                if username.lower() in forbidden_usernames:
                    return jsonify({"error": "Username is not allowed"}), 400

        webserver = request.json.get('webserver')
        mysql_type = request.json.get('sql_type')

        sql_flag = f"--sql={mysql_type}" if mysql_type and mysql_type in ["mysql", "mariadb"] else ""
        webserver_flag = f'--webserver="{webserver}"' if webserver and webserver in ["nginx", "apache", "openresty", "varnish+apache", "varnish+nginx", "varnish+openresty"] else ""

        command = f"opencli user-add {username} '{password}' {email} '{plan_name}' {webserver_flag} {sql_flag}"

        try:
            response = subprocess.check_output(command, shell=True, text=True)
            formatted_response = {'message': response.strip()}
            return jsonify({'success': True, 'response': formatted_response}), 201
        except subprocess.CalledProcessError as e:
            error_message = e.output.strip()
            return jsonify({'success': False, 'error': error_message}), 500

# ENDPOINT: /api/plans
# DESCRIPTION: list all plans or a specific plan by ID
# TYPE: GET
# EXAMPLES:
#
# curl -X GET http://localhost:2087/api/plans -H "Authorization: Bearer TOKEN_HERE"
#
@api.route('/plans', methods=['GET'])
@api.route('/plans/<int:plan_id>', methods=['GET'])
@csrf.exempt
@jwt_required()
def api_get_plans(plan_id=None):
    if plan_id is not None:
        plan = get_plan_by_id(plan_id)
        if plan is None:
            return jsonify({"error": "Plan not found"}), 404
        return jsonify({"plan": plan})
    else:
        plans = get_all_plans()
        return jsonify({"plans": plans})



# ENDPOINT: /api/services
# DESCRIPTION: list monitored services, check status, manage and edit them
# TYPE: GET PUT
# EXAMPLES: 
#
# curl -X GET http://localhost:2087/api/services -H "Authorization: Bearer YOUR_JWT_TOKEN"
#
# curl -X PUT http://localhost:2087/api/services -H "Authorization: Bearer YOUR_JWT_TOKEN"      -H "Content-Type: application/json" -d '{"service1": {"name": "Service One","status": "active"},"service2": {"name": "Service Two","status": "inactive"}}'
@api.route('/services', methods=['GET', 'PUT'])
@csrf.exempt
@jwt_required()
def api_services_from_file_and_check_status():

    SERVICES_FILE_PATH = '/etc/openpanel/openadmin/config/services.json'

    if request.method == 'GET':
        if os.path.exists(SERVICES_FILE_PATH):
            with open(SERVICES_FILE_PATH, 'r') as file:
                content = json.load(file)
            return jsonify(content)
        else:
            abort(404, description="File not found")

    elif request.method == 'PUT':
        if request.is_json:
            data = request.get_json()
            try:
                with open(SERVICES_FILE_PATH, 'w') as file:
                    json.dump(data, file, indent=4)
                return jsonify({"message": f"{SERVICES_FILE_PATH} updated successfully"}), 200
            except Exception as e:
                abort(500, description=str(e))
        else:
            abort(400, description="Request must be JSON")


# ENDPOINT: /api/services/status
# DESCRIPTION: check monitored services status
# TYPE: GET
# EXAMPLES: 
#
# curl -X GET http://localhost:2087/api/services/status -H "Authorization: Bearer YOUR_JWT_TOKEN"
@api.route('/services/status', methods=['GET'])
@csrf.exempt
@jwt_required()
def services_status_for_api():
    config_path = '/etc/openpanel/openadmin/config/services.json'

    try:
        with open(config_path, 'r') as f:
            services_config = json.load(f)
    except Exception as e:
        return jsonify({"error": f"Unable to read services.json file: {str(e)}"}), 500

    status_data = []  # Ensure this is initialized as a list

    def check_docker_status(container_name):
        docker_cmd = 'docker --context default ps --format "{{.Names}}"'
        docker_result = subprocess.run(docker_cmd, shell=True, stdout=subprocess.PIPE, stderr=subprocess.PIPE, text=True)
        container_names = docker_result.stdout.splitlines()
        return 'Active' if container_name in container_names else 'Inactive'


    for service in services_config:
        if service.get('on_dashboard'):
            try:
                if service['type'] == 'docker':
                    status = check_docker_status(service['real_name'])
                else:
                    cmd = f'systemctl is-active {service["real_name"]}.service'
                    result = subprocess.run(cmd, shell=True, stdout=subprocess.PIPE, stderr=subprocess.PIPE, text=True)
                    status = 'Active' if result.returncode == 0 else 'Inactive'
            except Exception as e:
                status = 'Error: ' + str(e)

            service_with_status = service.copy()
            service_with_status['status'] = status
            status_data.append(service_with_status)

    return jsonify(status_data)





# ENDPOINT: /api/services/<action>/<service_name>
# DESCRIPTION: start, restart, stop service
# TYPE: POST
# EXAMPLES: 
#
# curl -X POST http://localhost:2087/api/restart/caddy -H "Authorization: Bearer YOUR_JWT_TOKEN"
@api.route('/service/<action>/<service_name>', methods=['POST'])
@csrf.exempt
@jwt_required()
def manage_service_via_api(action, service_name):
    allowed_services = load_allowed_services()
    service_actions = ['start', 'restart', 'stop']

    if action not in service_actions:
        return jsonify({"error": f"Invalid action: {action}"}), 400

    if service_name not in allowed_services:
        return jsonify({"error": f"Invalid service: {service_name}"}), 400

    docker_services = {
        'openpanel_mysql': 'openpanel_mysql',
        'caddy': 'caddy',
        'openpanel': 'openpanel',
        'openpanel_dns': 'openpanel_dns',
        'mailserver': 'openadmin_mailserver',
        'roundcube': 'openadmin_roundcube'
    }

    try:
        if service_name in docker_services:
            if action == 'start':
                compose_command = f'cd /root && docker --context default compose up -d {docker_services[service_name]}'
            elif action == 'stop':
                compose_command = f'cd /root && docker --context default compose down {docker_services[service_name]}'
            elif action == 'restart':
                compose_command = f'cd /root && docker --context default compose down {docker_services[service_name]} && docker --context default compose up -d {docker_services[service_name]}'
            process = Popen(compose_command, shell=True, stdout=subprocess.PIPE, stderr=subprocess.PIPE)
        else:
            command = ['service', service_name, action]
            process = Popen(command, stdout=subprocess.PIPE, stderr=subprocess.PIPE)

        stdout, stderr = process.communicate()

        if process.returncode == 0:
            return jsonify({"success": f'{service_name.capitalize()} {action}ed successfully'})
        else:
            return jsonify({"error": f"Error {action}ing {service_name}: {stderr.decode().strip()}"}), 400
    except Exception as e:
        return jsonify({"error": f"Exception: {str(e)}"}), 500




# ENDPOINT: /api/docker/info
# DESCRIPTION: list docker info
# TYPE: GET
# EXAMPLES: 
#
# curl -X GET http://localhost:2087/api/docker/info -H "Authorization: Bearer YOUR_JWT_TOKEN"
@api.route('/docker/info', methods=['GET'])
@csrf.exempt
@jwt_required()
def api_docker_info():
    try:
        client = docker.from_env()
        docker_info = client.info()
        return jsonify(docker_info)
    except Exception as e:
        return jsonify({'error': str(e)}), 500


# ENDPOINT: /api/ips
# DESCRIPTION: list users with dedicated ip addresses
# TYPE: GET
# EXAMPLES: 
#
# curl -X GET http://localhost:2087/api/ips -H "Authorization: Bearer YOUR_JWT_TOKEN"
@api.route('/ips', methods=['GET'])
@csrf.exempt
@jwt_required()
def api_json_ips():
    users_list = get_all_users()
    users_ips = {}

    for user in users_list:
        username = user[1]
        ip_file_path = f'/etc/openpanel/openpanel/core/users/{username}/ip.json'

        if os.path.exists(ip_file_path):
            with open(ip_file_path, 'r') as ip_file:
                try:
                    ip_data = json.load(ip_file)
                    user_ip = ip_data.get('ip')
                    users_ips[username] = user_ip
                except json.JSONDecodeError as e:
                    print(f"Error decoding JSON for {username}: {str(e)}")

    return jsonify(users_ips)


# ENDPOINT: /api/system
# DESCRIPTION: list system information
# TYPE: GET
# EXAMPLES: 
#
# curl -X GET http://localhost:2087/api/system -H "Authorization: Bearer YOUR_JWT_TOKEN"
@api.route('/system', methods=['GET'])
@csrf.exempt
@jwt_required()
def api_system_info():
    info = {
        'hostname': platform.node(),
        'os': distro.name(pretty=True), # + " " + platform.release(),
        'time': time.strftime('%Y-%m-%d %H:%M:%S', time.localtime()),
        'kernel': platform.release(),
        'cpu': platform.processor(),
    }

    info['openpanel_version'] = get_openpanel_version()

    try:
        lscpu_output = subprocess.check_output(['lscpu']).decode('utf-8')
        for line in lscpu_output.split('\n'):
            if 'Model name:' in line:
                cpu_model = line.split(':')[1].strip()
                info['cpu'] = cpu_model + "(" + platform.processor() + ")"
                break
    except Exception as e:
        info['cpu'] = 'Unavailable'

    try:
        with open('/proc/uptime', 'r') as f:
            uptime_seconds = float(f.readline().split()[0])
            info['uptime'] = str(int(uptime_seconds))
    except Exception as e:
        info['uptime'] = 'Unavailable'
    
    try:
        ps_output = subprocess.check_output(['ps', '-e']).decode('utf-8')
        running_processes = ps_output.count('\n')
        info['running_processes'] = running_processes
    except Exception as e:
        info['running_processes'] = 'Unavailable'
    
    try:
        updates_output = subprocess.check_output(['apt', 'list', '--upgradable'], stderr=subprocess.DEVNULL).decode('utf-8')
        updates_count = updates_output.count('\n') - 1
        info['package_updates'] = updates_count
    except Exception as e:
        info['package_updates'] = 'Unavailable'
    
    return jsonify(info)





# ENDPOINT: /api/usage/cpu
# DESCRIPTION: display real-time cpu usage
# TYPE: GET
# EXAMPLES: 
#
# curl -X GET http://localhost:2087/api/usage/cpu -H "Authorization: Bearer YOUR_JWT_TOKEN"
@api.route('/usage/cpu', methods=['GET'])
@csrf.exempt
@jwt_required()
def api_system_cpu_usage():
    cpu_percent_per_core = psutil.cpu_percent(interval=1, percpu=True)

    cpu_info = {
        f"core_{i}": percent for i, percent in enumerate(cpu_percent_per_core)
    }

    return jsonify(cpu_info)




# ENDPOINT: /api/usage/memory
# DESCRIPTION: display real-time memory usage
# TYPE: GET
# EXAMPLES: 
#
# curl -X GET http://localhost:2087/api/usage/memory -H "Authorization: Bearer YOUR_JWT_TOKEN"
@api.route('/usage/memory', methods=['GET'])
@csrf.exempt
@jwt_required()
def api_system_memory_usage():
    ram_info = {
        "total": psutil.virtual_memory().total,
        "available": psutil.virtual_memory().available,
        "used": psutil.virtual_memory().used,
        "percent": psutil.virtual_memory().percent
    }

    human_readable_info = {
        "total": f"{ram_info['total'] / (1024 ** 3):.2f} GB",
        "available": f"{ram_info['available'] / (1024 ** 3):.2f} GB",
        "used": f"{ram_info['used'] / (1024 ** 3):.2f} GB",
        "percent": f"{ram_info['percent']}%"
    }

    return jsonify({"ram_info": ram_info, "human_readable_info": human_readable_info})




# ENDPOINT: /api/usage/disk
# DESCRIPTION: display real-time disk usage
# TYPE: GET
# EXAMPLES: 
#
# curl -X GET http://localhost:2087/api/usage/disk -H "Authorization: Bearer YOUR_JWT_TOKEN"
@api.route('/usage/disk', methods=['GET'])
@csrf.exempt
@jwt_required()
def api_system_disk_usage():
    partitions = psutil.disk_partitions(all=False)
    filtered_partitions = [partition for partition in partitions if not partition.mountpoint.startswith('/snap')]

    disk_usage_info = []
    for partition in filtered_partitions:
        disk = psutil.disk_usage(partition.mountpoint)
        disk_info = {
            'device': partition.device,
            'mountpoint': partition.mountpoint,
            'fstype': partition.fstype,
            'total': disk.total,
            'used': disk.used,
            'free': disk.free,
            'percent': disk.percent
        }
        disk_usage_info.append(disk_info)

    return jsonify(disk_usage_info)




app.register_blueprint(api) #register all api endpoints from this file
