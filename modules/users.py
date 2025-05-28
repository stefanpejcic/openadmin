################################################################################
# *************************************************************************
# *                                                                       *
# * OpenAdmin                                                             *
# * Copyright (c) OpenPanel. All Rights Reserved.                         *
# * Version: 1.3.3                                                        *
# * Build Date: 2025-05-28 10:37:26                                       *
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
# Last Modified: 07.10.2024
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




import os
import json
import socket
from flask import Flask, Response, abort, render_template, request, send_file, g, jsonify, session, url_for, flash, redirect, get_flashed_messages
from flask_login import current_user # needed for reseller checks!
import subprocess
import datetime
import requests
import psutil
from app import app, cache, is_license_valid, login_required_route, admin_required_route,load_openpanel_config, connect_to_database
import docker
from docker.errors import DockerException
import re

from modules.helpers import check_if_owner_for_user, get_all_users, query_context_by_username, get_plan_by_id, get_all_plans, get_userdata_by_username, get_hosting_plan_name_by_id, get_user_websites, gravatar_url



import os

def get_disk_usage(username=None):
    file_path = '/etc/openpanel/openpanel/core/users/repquota'
    disk_usage_data = {}

    try:
        print(f"Checking if file exists: {file_path}")
        if not os.path.exists(file_path):
            print("Error: repquota file does not exist.")
            return {}

        with open(file_path, 'r') as file:
            lines = file.readlines()
            print(f"Read {len(lines)} lines from {file_path}")

            for line in lines:
                print(f"Processing line: {line.strip()}")  # Print each line for debugging
                parts = line.split()

                # Ensure the line has enough parts and skip headers
                if len(parts) >= 8 and not parts[0].startswith("#") and parts[0] not in ("root", "User"):
                    user = parts[0]
                    try:
                        disk_usage_data[user] = {
                            "disk_used": int(parts[2]),
                            "disk_soft": int(parts[3]),
                            "disk_hard": int(parts[4]),
                            "inodes_used": int(parts[5]),
                            "inodes_soft": int(parts[6]),
                            "inodes_hard": int(parts[7])
                        }
                        print(f"Added disk usage for user {user}: {disk_usage_data[user]}")
                    except ValueError as ve:
                        print(f"Error converting values for user {user}: {ve}")

    except Exception as e:
        print(f"Error reading disk usage file: {str(e)}")

    if username:
        if "SUSPENDED_" in username:
            username = username.rsplit("_", 1)[-1]
        return disk_usage_data.get(username, "User not found")


    print(f"Final parsed disk usage data: {disk_usage_data}")
    return disk_usage_data


def get_usage_stats(username=None):
    base_path = "/etc/openpanel/openpanel/core/users/"
    all_stats = []
    
    try:
        # Get user directories
        user_dirs = [name for name in os.listdir(base_path) if os.path.isdir(os.path.join(base_path, name))]
        
        if username:
            if "SUSPENDED_" in username:
                username = username.rsplit("_", 1)[-1]
            user_dirs = [username] if username in user_dirs else []  # Filter for a specific user if provided

        for user_dir in user_dirs:
            file_path = os.path.join(base_path, user_dir, "docker_usage.txt")
            
            if os.path.exists(file_path):
                try:
                    with open(file_path, 'r') as file:
                        lines = file.readlines()
                        last_line = lines[-1] if lines else None

                    if last_line:
                        date_part, json_data = last_line.split(" ", 1)
                        stats = {"date": date_part, "stats": json.loads(json_data), "user": user_dir}
                        all_stats.append(stats)
                except Exception as file_error:
                    print(f"Error reading file {file_path}: {str(file_error)}")
        
    except Exception as e:
        print(f"Unexpected error: {str(e)}")
    
    # Return stats for a specific user if `username` is provided
    if username:
        return all_stats[0] if all_stats and all_stats[0]['user'] == username else "no data yet"
    
    return all_stats


def log_user_action(username, action):
    if username.startswith(('SUSPENDED_', 'suspended_')):
        username = username.rsplit('_', 1)[-1]
    username = username.replace(" ", "")
    log_dir = f"/etc/openpanel/openpanel/core/users/{username}"
    log_file = os.path.join(log_dir, f'activity.log')
    ip_address = request.headers.get('CF-Connecting-IP')
    if ip_address is None:
        ip_address = request.remote_addr

    request.headers
    
    timestamp = datetime.datetime.now().strftime('%Y-%m-%d %H:%M:%S')
    log_entry = f'{timestamp}  {ip_address} {action} \n'
    
    if not os.path.exists(log_dir):
        os.makedirs(log_dir)
    
    with open(log_file, 'a') as f:
        f.write(log_entry)







@app.route('/client/disk/<container_name>', methods=['GET'])
@login_required_route
@cache.memoize(timeout=600)
def disk_info_container(container_name):

    if not check_if_owner_for_user(container_name):
        abort(403)

    docker_context = query_context_by_username(container_name)

    try:
        docker_command = ['docker', '--context', docker_context, 'system', 'df', '-v', '--format', '{{json .}}']

        inspect_output = subprocess.check_output(docker_command)

        # Split the output into lines and parse each line individually
        inspect_data = []
        for line in inspect_output.decode('utf-8').splitlines():
            inspect_data.append(json.loads(line))

        return jsonify(inspect_data)
    except Exception as e:
        return jsonify({'error': str(e)}), 500


@app.route('/get_user_stats', methods=['POST'])
@login_required_route
@cache.memoize(timeout=600)
def get_user_stats():
    username = request.form.get('username')

    if not username:
        return jsonify({"error": "Username not provided"}), 400

    if not check_if_owner_for_user(username):
        abort(403)

    user_stats_dir = f'/etc/openpanel/openpanel/core/stats/{username}/'
    if not os.path.exists(user_stats_dir):
        return jsonify({"error": f"No stats found for user {username}"}), 404

    # Read and combine all JSON files with timestamp
    combined_stats = []
    for filename in os.listdir(user_stats_dir):
        if filename.endswith('.json'):
            timestamp = filename[:-5]  # Remove ".json" extension
            file_path = os.path.join(user_stats_dir, filename)
            with open(file_path, 'r') as file:
                stats = json.load(file)
                stats['timestamp'] = timestamp
                combined_stats.append(stats)

    return jsonify(combined_stats)


@cache.memoize(timeout=3600)
def get_hostname():
    try:
        hostname = socket.gethostname()
        return hostname
    except Exception as e:
        return f"Error: {e}"


@cache.memoize(timeout=60)
def read_env_file(username):

    context = query_context_by_username(username)

    if context is None:
        if "SUSPENDED_" in username:
            username = username.rsplit("_", 1)[-1]
        return {"error": "No context found for user", "details": f"username: {username}"}

    file_path = f"/home/{context}/.env"
    env_data = {}
    if os.path.exists(file_path):
        with open(file_path, "r") as file:
            for line in file:
                line = line.strip()
                if line and not line.startswith("#"):  # Ignore comments and empty lines
                    key, value = line.split("=", 1)
                    env_data[key.strip()] = value.strip().strip('"')
    return env_data

def get_first_ip(current_username):
    dedicated_ip_file_path = f"/etc/openpanel/openpanel/core/users/{current_username}/ip.json"

    if os.path.exists(dedicated_ip_file_path):
        try:
            with open(dedicated_ip_file_path, 'r') as file:
                data = json.load(file)
                ip = data.get("ip", "")
                if not ip:  # if IP is empty, fallback
                    ip = get_system_ip_from_hostname_cmd()
        except json.JSONDecodeError:
            ip = get_system_ip_from_hostname_cmd()
    else:
        ip = get_system_ip_from_hostname_cmd()
    return ip


@cache.memoize(timeout=3600)
def get_system_ip_from_hostname_cmd()
    try:
        output = subprocess.check_output(["hostname", "-I"]).decode("utf-8").strip()
        ips = output.split()
        return ips[0] if ips else "Unknown"
    except subprocess.CalledProcessError:
        return "Unknown"



@app.route('/dns-bind/<domain>', methods=['GET'])
@login_required_route
@cache.memoize(timeout=300)
def get_dns_bind_content(domain):
    # Define the path to the BIND file
    bind_file_path = f'/etc/bind/zones/{domain}.zone'

    # Check if the file exists
    if os.path.exists(bind_file_path):
        # Read the content of the BIND file
        with open(bind_file_path, 'r') as file:
            bind_content = file.read()

        # Return the content as JSON
        return jsonify({'domain': domain, 'bind_content': bind_content})
    else:
        return jsonify({'error': 'Domain not found'}), 404



@app.route('/save-dns/<domain>', methods=['POST'])
@login_required_route
def save_dns(domain):
    try:
        new_content = request.json['new_content']
        with open(f'/etc/bind/zones/{domain}.zone', 'w') as file:
            file.write(new_content)
        subprocess.run(['docker', 'restart', 'openpanel_dns'])
        return jsonify({'success': True, 'message': 'DNS content saved successfully'})
    except Exception as e:
        return jsonify({'success': False, 'error': str(e)}), 500



@app.route('/caddy-logs/<domain>', methods=['GET'])
@login_required_route
@cache.memoize(timeout=60)
def get_log_caddy_content(domain):
    caddy_file_path = f'/var/log/caddy/domlogs/{domain}/access.log'
    if os.path.exists(caddy_file_path):
        with open(caddy_file_path, 'r') as file:
            lines = file.readlines()
            last_50_lines = lines[-50:]
            caddy_content = ''.join(last_50_lines)
        return jsonify({'domain': domain, 'caddy_content': caddy_content})
    else:
        return jsonify({'error': 'Domain not found'}), 404


@app.route('/caddy-vhosts/<domain>', methods=['GET'])
@login_required_route
@cache.memoize(timeout=300)
def get_dns_caddy_content(domain):
    caddy_file_path = f'/etc/openpanel/caddy/domains/{domain}.conf'
    if os.path.exists(caddy_file_path):
        with open(caddy_file_path, 'r') as file:
            caddy_content = file.read()
        return jsonify({'domain': domain, 'caddy_content': caddy_content})
    else:
        return jsonify({'error': 'Domain not found'}), 404

@app.route('/save-vhosts/<domain>', methods=['POST'])
@login_required_route
def save_caddy_vhosts(domain):
    try:
        new_content = request.json['new_content']
        with open(f'/etc/openpanel/caddy/domains/{domain}.conf', 'w') as file:
            file.write(new_content)
        os.system('docker exec caddy caddy reload --config /etc/caddy/Caddyfile')
        return jsonify({'success': True, 'message': 'Caddy configuration file saved successfully'})
    except Exception as e:
        return jsonify({'success': False, 'error': str(e)}), 500






def parse_log_line(log_line):
    # Define regex pattern to extract components from the log line
    pattern = r"(\d{4}-\d{2}-\d{2} \d{2}:\d{2}:\d{2})\s+(\d+\.\d+\.\d+\.\d+)\s+(User|Administrator)\s+(\S+)\s+(.*)"
    
    match = re.match(pattern, log_line)
    if match:
        timestamp, ip_address, user_type, user, action = match.groups()
        return {
            "timestamp": timestamp,
            "ip_address": ip_address,
            "user_type": user_type,
            "user": user,
            "action": action
        }
    return None





@app.route('/user_activity/<username>', methods=['GET'])
@login_required_route
@cache.memoize(timeout=300)
def get_user_activity(username):
    if "SUSPENDED_" in username:
        username = username.rsplit("_", 1)[-1]

    if not check_if_owner_for_user(username):
        abort(403)
    
    log_file_path = f'/etc/openpanel/openpanel/core/users/{username}/activity.log'
    
    
    try:
        with open(log_file_path, 'r') as file:
            content = file.readlines()
            reversed_content = reversed(content)
            
            # Parse each line into a structured format
            parsed_activity = [
                parse_log_line(line.strip()) for line in reversed_content if line.strip()
            ]
            
            # Remove any None results if parsing fails for any line
            parsed_activity = [entry for entry in parsed_activity if entry is not None]
            
            return jsonify({'user_activity': parsed_activity})

    except FileNotFoundError:
        return jsonify({'error': f'Activity log for user {username} not found'}), 404

@cache.memoize(timeout=300)
def get_containers(username):
    context = query_context_by_username(username)

    if "SUSPENDED_" in username:
        username = username.rsplit("_", 1)[-1]

    if context is None:
        return {"error": "No context found for user", "details": f"username: {username}"}

    # Run the subprocess command
    try:
        result = subprocess.run(
            ["docker", "--context", context, "compose", "-f", f"/home/{context}/docker-compose.yml", "config", "--format", "json"],
            capture_output=True,
            text=True,
            check=True
        )
        docker_data = json.loads(result.stdout)  # Parse JSON output
    except subprocess.CalledProcessError as e:
        docker_data = {"error": "Failed to fetch container data", "details": str(e)}

    if isinstance(docker_data, dict) and 'services' in docker_data:
        # Access the services dictionary
        services = docker_data['services']
        docker_data['services'] = services
    else:
        docker_data = {"error": "Invalid data format", "details": "docker_data does not contain 'services'."}

    return docker_data



def read_env_values():
    env_path = '/etc/openpanel/docker/compose/1.0/.env'
    values = {
        'MYSQL_TYPE': None,
        'WEB_SERVER': None
    }
    try:
        with open(env_path, 'r') as file:
            for line in file:
                line = line.strip()
                if line.startswith('MYSQL_TYPE='):
                    values['MYSQL_TYPE'] = line.split('=', 1)[1].strip().strip('"').strip("'")
                elif line.startswith('WEB_SERVER='):
                    values['WEB_SERVER'] = line.split('=', 1)[1].strip().strip('"').strip("'")
    except FileNotFoundError:
        return None
    return values

@app.route('/user/new', methods=['GET', 'POST'])
@login_required_route
def create_user():
    if request.method == 'GET':
        current_route = request.path
        hosting_plans = get_all_plans()
        plans = hosting_plans if hosting_plans else []
        # keep for import!
        #users_list = get_all_users()
        #users = users_list if users_list else []
        messages = get_flashed_messages()       
        defaults = read_env_values()     
        return render_template('users/add.html', 
            title='Add User', 
            messages=messages, 
            gravatar_url=gravatar_url, 
            plans=plans, 
            #users=users,
            app=app, 
            defaults=defaults,
            current_route=current_route
            )

    if request.method == 'POST':
        
        email = request.form.get('admin_email')
        username = request.form.get('admin_username').lower()
        password = request.form.get('admin_password')
        plan_name = request.form.get('plan_name')
        send_mail = request.form.get('send_email')

        webserver = request.form.get('webserver')
        mysql_type = request.form.get('sql_type')

        slave = request.form.get('slave')
        ssh_key_path = request.form.get('ssh_key_path')

        reseller_flag = ""
        server_flag = ""
        
        if not email:
            email = username

        # add reseller user as owner in back only!
        user_role = getattr(current_user, 'role')
        if user_role == 'reseller':
            owner = getattr(current_user, 'username')
            reseller_flag = f"--reseller={owner}"

        send_mail_flag = f"--send-email" if send_mail else ""
        sql_flag = f"--sql={mysql_type}" if mysql_type and mysql_type in ["mysql", "mariadb"] else ""
        webserver_flag = f'--webserver="{webserver}"' if webserver and webserver in ["nginx", "apache", "openresty", "varnish+apache", "varnish+nginx", "varnish+openresty"] else ""

        if slave and re.match(r'^\d+\.\d+\.\d+\.\d+$', slave):
            server_flag = f"--server={slave}"

        key_flag = f"--key={ssh_key_path}" if ssh_key_path else ""

        command = f"opencli user-add {username} '{password}' {email} '{plan_name}' {reseller_flag} {webserver_flag} {sql_flag} {send_mail_flag} {server_flag} {key_flag} --debug"

        # Run the command and yield its output
        def generate():
            process = subprocess.Popen(command, shell=True, stdout=subprocess.PIPE, stderr=subprocess.PIPE, text=True)

            try:
                for line in iter(process.stdout.readline, ''):
                    yield f"{line.strip()}\n"

                process.stdout.close()
                process.wait()

            except Exception as e:
                yield f"Error: {str(e)}\n\n"
            finally:
                process.stderr.close()
        return Response(generate(), mimetype='text/event-stream')


# HELPER FOR GEIP API
@cache.memoize(timeout=3600)
def is_geoip_installed():
    try:
        # apt-get install geoip-bin
        subprocess.check_output(['which', 'geoiplookup'])
        return True
    except subprocess.CalledProcessError:
        return False


def get_ipv4_or_ipv6(server_ip):
    if re.match(r'^\d+\.\d+\.\d+\.\d+$', server_ip):
        ip_type = 'IPv4'
    elif re.match(r'^[0-9a-fA-F:]+$', server_ip):
        ip_type = 'IPv6'
    else:
        ip_type = 'Invalid'
    return ip_type


def get_country_info(server_ip):
    if is_geoip_installed():
        try:
            ip_lookup_result = subprocess.check_output(['geoiplookup', server_ip]).decode('utf-8')

            # on ipv6 errors fallback to the api service
            if "can't resolve hostname" in ip_lookup_result or "GeoIP Country Edition: IP Address not found" in ip_lookup_result:
                return get_country_info_from_api(server_ip)
            
            country_info = ip_lookup_result.split(': ')[-1].strip()
            return tuple(country_info.split(', '))

        except subprocess.CalledProcessError as e:
            print(f"Error executing geoiplookup: {e}")
            return get_country_info_from_api(server_ip)
    else:
        return get_country_info_from_api(server_ip)

def get_country_info_from_api(server_ip):
    try:
        response = requests.get(f"https://api.openpanel.com/countrycode/{server_ip}")
        response.raise_for_status()
        data = response.json()
        return data.get("country"), None
    except requests.RequestException as e:
        print(f"Error fetching country information from remote service: {e}")
        return None, None



@app.route('/get_custom_message_for_user/<username>', methods=['GET', 'POST'])
@login_required_route
def manage_custom_message(username):
    if not check_if_owner_for_user(username):
        abort(403)

    custom_message_file_path = f"/etc/openpanel/openpanel/core/users/{username}/custom_message.html"
    
    if request.method == 'GET':
        if os.path.exists(custom_message_file_path):
            with open(custom_message_file_path, 'r') as file:
                custom_message = file.read()
            return jsonify({'custom_message': custom_message}), 200
        else:
            return jsonify({'message': 'No custom message found'}), 200

    elif request.method == 'POST':
        if request.is_json:
            data = request.get_json()
            custom_message = data.get('custom_message')
            if custom_message is not None and custom_message != "":
                os.makedirs(os.path.dirname(custom_message_file_path), exist_ok=True)
                with open(custom_message_file_path, 'w') as file:
                    file.write(custom_message)
                return jsonify({'message': f'Custom message for user {username} saved successfully.'}), 200
            else:
                os.makedirs(os.path.dirname(custom_message_file_path), exist_ok=True)
                with open(custom_message_file_path, 'w') as file:
                    file.write('') 
                return jsonify({'message': f'Custom message for user {username} deleted successfully.'}), 200
        else:
            return jsonify({'error': 'Invalid content type. Expected JSON.'}), 400

# USERS ROUTE
@app.route('/users', defaults={'username': None}, methods=['GET'])
@app.route('/users/', defaults={'username': None}, methods=['GET'])
@app.route('/users/<username>', methods=['GET'])
@login_required_route
def users(username):
    current_route = request.path
    output_format = request.args.get('output')
    mysql_is_down = False

    if username:
        if not check_if_owner_for_user(username):
            abort(403)

        user = get_userdata_by_username(username)
        if user:
            if user == -1:
                flash(f'Error: MySQL service is down, Check service status.', 'error')
                return redirect(url_for('users'))

            server_ip = get_first_ip(username)
            country_code, country_name = get_country_info(server_ip)
            server_name = get_hostname()
            ip_type = get_ipv4_or_ipv6(server_ip)

            docker_data = get_containers(username)

            hosting_plans = get_all_plans()
            plans = hosting_plans if hosting_plans else []

            env_data = read_env_file(username)

            usage_stats = get_usage_stats(username)
            disk_usage = get_disk_usage(username)

            if output_format == 'json':
                return jsonify({
                    'username': username,
                    'server_ip': server_ip,
                    'server_name': server_name,
                    'env_data': env_data,
                    'ip_type': ip_type,
                    'disk': disk_usage,
                    'stats': usage_stats,
                    'user': user,
                    'country_code': country_code,
                    'country_name': country_name,
                    'docker_data': docker_data,
                    'plans': plans
                })
            else:
                return render_template('users/single.html', 
                    title=f'User: {username}', 
                    server_ip=server_ip, 
                    server_name=server_name, 
                    ip_type=ip_type,
                    user=user, 
                    app=app, 
                    current_route=current_route, 
                    country_code=country_code, 
                    country_name=country_name, 
                    gravatar_url=gravatar_url, 
                    env_data=env_data,
                    stats=usage_stats,
                    disk=disk_usage,
                    get_user_websites=get_user_websites, 
                    docker_data=docker_data,
                    plans=plans, # sued on edit only, todo use js!
                    get_hosting_plan_name_by_id=get_hosting_plan_name_by_id
                )
        else:
            message = f'Error: User {username} not found'
            if output_format == 'json':
                return jsonify({
                        'message': message,
                        'status': 'error'
                    })
            flash(f'Error: User {username} not found', 'error')
            return redirect(url_for('users'))
    else:
        users = get_all_users()
        if users == -1:
            users = []
            plans = []
            mysql_is_down = True
        else:
            plans = get_all_plans()

        usage_stats = get_usage_stats()
        disk_usage = get_disk_usage()

        if output_format == 'json': # get only users

            return jsonify({
                    'users': users,
                    'stats': usage_stats,
                    'disk': disk_usage
                })
        else:
            messages = get_flashed_messages()            
            return render_template('users/all.html', 
                title='Users', 
                messages=messages, 
                gravatar_url=gravatar_url, 
                users=users, 
                plans=plans, 
                stats=usage_stats,
                disk=disk_usage,
                app=app, 
                current_route=current_route,
                mysql_is_down=mysql_is_down
            )

# returns a list of users with dedicated ip addresses
@app.route('/json/ips', methods=['GET'])
@admin_required_route
@cache.memoize(timeout=600)
def json_ips():
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

def parse_df_output(output):
    lines = output.strip().split('\n')
    last_line = lines[-1]
    penultimate_line = lines[-2]
    
    columns_last = last_line.split()
    columns_penultimate = penultimate_line.split()

    if len(columns_last) == 5 and len(columns_penultimate) == 5:
        df_dict = {
            "itotal_last": columns_last[0],
            "iused_last": columns_last[1],
            "size_last": columns_last[2],
            "used_last": columns_last[3],
            "target_last": columns_last[4],
            "itotal_penultimate": columns_penultimate[0],
            "iused_penultimate": columns_penultimate[1],
            "size_penultimate": columns_penultimate[2],
            "used_penultimate": columns_penultimate[3],
            "target_penultimate": columns_penultimate[4]
        }
        return df_dict
    else:
        return None

@app.route('/container/disk_inodes/<username>')
@login_required_route
@cache.memoize(timeout=300)
def disk_inodes_route(username):
    if not username:
        abort(401, "User not authenticated")

    if not check_if_owner_for_user(username):
        abort(403)

    file_path = '/etc/openpanel/openpanel/core/users/repquota'

    try:
        # repquota -u / > /etc/openpanel/openpanel/core/users/repquota
        find_user_in_file = f"grep '{username} ' {file_path}"
        disk_usage_result = subprocess.check_output(find_user_in_file, shell=True).decode('utf-8').strip()
    except subprocess.CalledProcessError as e:
        return jsonify({"error": "Failed to retrieve disk usage information."}), 500

    if not disk_usage_result:
        return jsonify({"error": "Disk usage data not found."}), 404

    disk_usage_parts = disk_usage_result.split()
    # example: "milenko   -- 1599556 10240000 10240000          41990 1000000 1000000"

    disk_used = int(disk_usage_parts[2])
    disk_soft = int(disk_usage_parts[3])
    disk_hard = int(disk_usage_parts[4])
    inodes_used = int(disk_usage_parts[5])
    inodes_soft = int(disk_usage_parts[6])
    inodes_hard = int(disk_usage_parts[7])

    formatted_output = {
        "inodes_used": inodes_used,
        "inodes_soft": inodes_soft,
        "inodes_hard": inodes_hard,
        "disk_used": disk_used,
        "disk_soft": disk_soft,
        "disk_hard": disk_hard,
        "home_path": f"/var/www/html/"
    }

    return jsonify(formatted_output)


@cache.memoize(timeout=300)
def get_container_stats(container_name):
    docker_context = query_context_by_username(container_name)
    if not docker_context:
        docker_context = container_name

    try:
        docker_cmd = ['docker', '--context', docker_context, 'stats', '--no-stream', '--format', '{{json .}}', container_name]

        # Run the Docker command and capture the output
        result = subprocess.run(docker_cmd, capture_output=True, text=True, check=True)

        # Parse the JSON output
        container_stats = json.loads(result.stdout)

        # Extract necessary information from the stats
        cpu_percent = container_stats.get('CPUPerc', '0.00').strip('%')
        
        # Parse memory usage and limit values, stripping the units
        mem_usage = container_stats.get('MemUsage', '0/0').split(' / ')[0].strip()
        mem_limit = container_stats.get('MemUsage', '0/0').split(' / ')[1].strip()
        mem_percent = container_stats.get('MemPerc', '0.00').strip('%')

        return {
            'CPU %': f'{cpu_percent}%',
            'Memory Usage': f'{mem_usage}',
            'Memory Limit': f'{mem_limit}',
            'Memory %': f'{mem_percent}%',
        }

    except subprocess.CalledProcessError as e:
        return {
            "status": "error",
            "message": f"Failed to get stats for container '{container_name}'. Docker command error."
        }, 503

    except json.JSONDecodeError:
        return {
            "status": "error",
            "message": "Failed to parse Docker stats output."
        }, 500

    except Exception as e:
        return {
            "status": "error",
            "message": f"An error occurred: {e}"
        }, 500


def calculate_cpu_percent(stats):
    # Extract CPU stats
    cpu_delta = stats['cpu_stats']['cpu_usage']['total_usage'] - stats['precpu_stats']['cpu_usage']['total_usage']
    system_delta = stats['cpu_stats']['system_cpu_usage'] - stats['precpu_stats']['system_cpu_usage']
    
    # Get the number of CPUs
    num_cpus = stats['cpu_stats']['online_cpus']

    if system_delta > 0 and cpu_delta > 0:
        cpu_percent = (cpu_delta / system_delta) * num_cpus * 100.0
    else:
        cpu_percent = 0.0

    return cpu_percent



# suspend, unsuspend, edit,  delete
@app.route('/user/<action>/<username>', methods=['GET', 'POST'])
@login_required_route
def manage_user(action, username):

    if not check_if_owner_for_user(username):
        abort(403)

    if action == 'suspend':
        command = f"opencli user-suspend '{username}'"
        success_message = f"User '{username}' suspended successfully"
        error_message = f"Error suspending user {username}"
        try:
            result = subprocess.check_output(command, shell=True, text=True)
            if "successfully" in result:
                log_action = f"Administrator {current_user.username} suspended user {username}"
                log_user_action(username, log_action)
                flash(success_message)
                return redirect(url_for('users'))
            else:
                flash(error_message)
                return redirect(url_for('users'))
        except subprocess.CalledProcessError as e:
            return jsonify({"message": f"Error executing command for user '{username}': {e.output}"})
            
        return redirect(url_for('users'))

    elif action == 'unsuspend':
        # fix for suspended usernames
        username_underscore_index = username.rfind('_')
        if username_underscore_index != -1:
            username = username[username_underscore_index + 1:]
        command = f"opencli user-unsuspend '{username}'"
        success_message = f"User '{username}' unsuspended successfully"
        error_message = f"Error unsuspending user {username}"
        try:
            result = subprocess.check_output(command, shell=True, text=True)
            if "success" in result:
                log_action = f"Administrator {current_user.username} unsuspended user {username}"
                log_user_action(username, log_action)
                flash(success_message)
                return redirect(url_for('users'))
            else:
                flash(error_message)
                return redirect(url_for('users'))
        except subprocess.CalledProcessError as e:
            jsonify({"message": f"Error executing command for user '{username}': {e.output}"})
            return redirect(url_for('users'))


    elif action == 'edit':
        error_message = f"Error editing user {username}"
        new_username = request.form.get('new_username')
        new_email = request.form.get('new_email')
        new_plan_id = request.form.get('plan_id')
        new_password = request.form.get('new_password')
        new_ip = request.form.get('new_ip')
        # Get user data
        user = get_userdata_by_username(username)
        if user:
            old_plan_id = user["plan_id"]
            plan_data = get_plan_by_id(old_plan_id)
            new_plan_data = get_plan_by_id(new_plan_id)
            old_plan_name = plan_data["name"]
            new_plan_name = new_plan_data["name"]
            old_email = user["email"]
            old_ip = get_first_ip(username)
            print("Old Plan Name:", old_plan_name)
            print("Old Email:", old_email)
            print("Old IP:", old_ip)

            # Compare the submitted data with existing user data
            if (new_username != username or
                new_email != old_email or
                new_plan_name != old_plan_name or
                new_password or
                new_ip != old_ip):

                # Execute opencli commands based on provided data..
                '''
                if new_ip != old_ip:
                    print("old IP:", old_ip)
                    print("new IP:", new_ip)
                    print(f"Changing user IP from {old_ip} to {new_ip}")
                    run_command = f"opencli user-ip {username} {new_ip} -y"
                    try:
                        result = subprocess.check_output(run_command, shell=True, text=True)
                        if "successfully" in result:
                            log_action = f"Administrator {current_user.username} changed IP address for user"
                            log_user_action(username, log_action)
                            pass
                        else:
                            return jsonify({"message": error_message})
                    except subprocess.CalledProcessError as e:
                        return jsonify({"message": f"Error executing command {run_command} : {e.output}"})
                '''

                if new_email != old_email:
                    print("old email:", old_email)
                    print("new email:", new_email)
                    print(f"Changing user email from {old_email} to {new_email}")
                    run_command = f"opencli user-email {username} {new_email}"
                    try:
                        result = subprocess.check_output(run_command, shell=True, text=True)
                        if "Email for user" in result:
                            log_action = f"Administrator {current_user.username} changed the email address for user"
                            log_user_action(username, log_action)
                            pass
                        else:
                            return jsonify({"message": error_message})
                    except subprocess.CalledProcessError as e:
                        return jsonify({"message": f"Error executing command {run_command} : {e.output}"})


                if new_password:
                    print("new password:", new_password)
                    print(f"Changing user password to {new_password}")
                    run_command = f"opencli user-password {username} {new_password} --ssh"
                    try:
                        result = subprocess.check_output(run_command, shell=True, text=True)
                        if "Successfully" in result:
                            log_action = f"Administrator {current_user.username} changed password for user {username}"
                            log_user_action(username, log_action)
                            pass
                        else:
                            return jsonify({"message": error_message})
                    except subprocess.CalledProcessError as e:
                        return jsonify({"message": f"Error executing command {run_command} : {e.output}"})

                if new_plan_name and old_plan_name and new_plan_name != old_plan_name:
                    print("old plan name:", old_plan_name)
                    print("new plan name:", new_plan_name)
                    print(f"Changing user plan from {old_plan_name} to {new_plan_name}")
                    run_command = f"opencli user-change_plan {username} '{new_plan_name}'"
                    try:
                        result = subprocess.check_output(run_command, shell=True, text=True)
                        if "success" in result:
                            log_action = f"Administrator {current_user.username} changed plan for user"
                            log_user_action(username, log_action)
                            pass
                        else:
                            return jsonify({"message": error_message})
                    except subprocess.CalledProcessError as e:
                        return jsonify({"message": f"Error executing command {run_command} : {e.output}"})


                if new_username != username:
                    print("old username:", username)
                    print("new username:", new_username)
                    print(f"Changing username from {username} to {new_username}")
                    run_command = f"opencli user-rename {username} {new_username}"
                    try:
                        result = subprocess.check_output(run_command, shell=True, text=True)
                        if "success" in result:
                            log_action = f"Administrator {current_user.username} changed username for user"
                            log_user_action(username, log_action)
                            pass
                        else:
                            return jsonify({"message": error_message})
                    except subprocess.CalledProcessError as e:
                        return jsonify({"message": f"Error executing command {run_command} : {e.output}"})

                # if any changed:
                flash(f"User '{username}' updated successfully")

                # if /users/<username>
                if '/users' in request.referrer and username in request.referrer:
                    # if username changed, take him to new username page
                    if new_username != username:
                        return redirect(url_for('users', username=new_username))
                    # if old username
                    else:
                        return redirect(url_for('users', username=username))
                # if not /users/<username>
                return redirect(url_for('users'))
                
            else:
                flash("No data provided to change")
                return redirect(url_for('users'))
        else:
            flash(f"User {username} not found")
            return redirect(url_for('users'))

    elif action == 'delete':
        command = f"opencli user-delete '{username}' -y"
        success_message = f"User '{username}' deleted successfully"
        error_message = f"Error deleting user {username}"
        try:
            result = subprocess.check_output(command, shell=True, text=True)
            if "successfully" in result:
                flash(success_message)
            else:
                flash(error_message)

                return redirect(url_for('users'))

        except subprocess.CalledProcessError as e:
            return jsonify({"message": f"Error executing command for user '{username}': {e.output}"})
            #return redirect(url_for('users'))
    else:
        flash("Invalid user action, valid options are: edit, suspend, unsuspend, delete")
    return redirect(url_for('users'))





@cache.memoize(timeout=60)
def get_all_containers_stats(context):

    try:
        # Get stats for all containers using docker stats
        docker_command = f"docker --context {context} stats --all --no-stream --format '{{{{json .}}}}'"
        result = subprocess.run(docker_command, shell=True, capture_output=True, text=True)

        if result.returncode == 0:
            # Parse the JSON output for each container
            container_stats = []

            # If the result stdout is empty, return an empty list
            if not result.stdout.strip():
                return container_stats

            for line in result.stdout.strip().split('\n'):
                try:
                    container_stat = json.loads(line)
                    container_stats.append(container_stat)
                except json.JSONDecodeError as e:
                    # Handle JSON parsing error
                    print(f"Error parsing container stats JSON: {str(e)}")
                    continue

            return container_stats

        else:
            # Command failed, log the error message
            print(f"Error retrieving container stats for context: {context}: {result.stderr}")
            return None
    except subprocess.SubprocessError as e:
        # Handle subprocess-related errors
        print(f"Subprocess error when fetching container stats: {str(e)}")
        return None
    except Exception as e:
        # Handle any other unexpected errors
        print(f"Unexpected error when fetching container stats: {str(e)}")
        return None




@app.route('/containers/stats/<username>')
@login_required_route
@cache.memoize(timeout=30)
def containers_stats(username):
    context = query_context_by_username(username) 

    # Get the stats for all containers
    container_stats = get_all_containers_stats(context)

    if container_stats is not None:
        if len(container_stats) > 0:
            return jsonify({'container_stats': container_stats})
        else:
            # Handle case where no data was returned
            container_stats = []
            return jsonify({'container_stats': container_stats})
    else:
        # If the function returns None, something went wrong (command failed)
        return jsonify({'error': 'Could not retrieve container stats'}), 500













# Define file paths
file_paths = {
    'compose': '/etc/openpanel/docker/compose/1.0/docker-compose.yml',
    'env': '/etc/openpanel/docker/compose/1.0/.env'
}


# Function to read file content
def read_file(file_path):
    if os.path.exists(file_path):
        with open(file_path, 'r') as f:
            return f.read()
    else:
        return None

# Function to write content to a file
def write_file(file_path, content):
    with open(file_path, 'w') as f:
        f.write(content)



@app.route('/user/templates', methods=['GET', 'POST'])
@admin_required_route
def user_defaults():
    current_route = request.path

    if request.method == 'POST':
        # Update files with posted content
        env = request.form.get('env')
        compose = request.form.get('compose')

        # Write the new content to files
        if env is not None:
            write_file(file_paths['env'], env)
        if compose is not None:
            write_file(file_paths['compose'], compose)

        flash("Files updated successfully!", "success")

    file_contents = {}
    for key, path in file_paths.items():
        file_contents[key] = read_file(path) or ''

    if request.args.get('output') == 'json':
        return jsonify(results)
    return render_template('users/templates.html', title='User Templates', current_route=current_route, **file_contents)
