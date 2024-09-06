################################################################################
# Author: Stefan Pejcic
# Created: 11.07.2023
# Last Modified: 30.05.2024
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
from flask_login import current_user
import subprocess
import datetime
import requests
import psutil
from app import app, is_license_valid, login_required_route, load_openpanel_config, connect_to_database
import docker
from docker.errors import DockerException
import re

from modules.helpers import get_all_users, get_user_and_plan_count, get_plan_by_id, get_all_plans, get_userdata_by_username, get_hosting_plan_name_by_id, get_user_websites, is_username_unique, gravatar_url

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



@app.route('/json/user/sudo/<container_name>', methods=['GET', 'POST'])
@login_required_route
def check_sudo(container_name):
    try:

        client = docker.from_env()

        # Sanitize container_name to avoid security risks
        container_name = re.sub(r'[^a-zA-Z0-9_.-]', '', container_name)
        container = client.containers.get(container_name)

        if request.method == 'GET':
            # Execute command inside the Docker container
            exec_command = "cat /etc/entrypoint.sh"
            exit_code, output = container.exec_run(exec_command)

            # Check if the command was successful
            if exit_code != 0:
                return jsonify({"error": f"Failed to check sudo right for the user {container_name}, command execution failed"}), 500

            # Check for SUDO setting
            output_str = output.decode('utf-8')
            sudo_match = re.search(r'SUDO="(YES|NO)"', output_str)
            if sudo_match:
                sudo_status = sudo_match.group(1)
                return jsonify({"username": container_name, "sudo": sudo_status})
            else:
                return jsonify({"error": f"SUDO setting not found in /etc/entrypoint.sh for user {container_name}"}), 404

        elif request.method == 'POST':
            action = request.json.get('action')
            if action not in ['enable', 'disable']:
                return jsonify({"error": "Invalid action. Must be 'enable' or 'disable'."}), 400

            # Map action to command
            sudo_action = "enable" if action == "enable" else "disable"
            exec_command = f"opencli user-sudo {container_name} {sudo_action}"
            exit_code, output = container.exec_run(exec_command)

            # Check if the command was successful
            if exit_code != 0:
                return jsonify({"error": f"Failed to {sudo_action} sudo for user {container_name}, command execution failed"}), 500

            return jsonify({"message": f"Sudo {sudo_action}d for user {container_name} successfully."}), 200

    except DockerException as e:
        return jsonify({"status": "error", "message": "Docker is unavailable. Please check the Docker service."}), 503

    except docker.errors.NotFound:
        return jsonify({"error": f"Container for user {container_name} not found"}), 404

    except Exception as e:
        # Add logging for debugging
        app.logger.error(f"Unexpected error: {str(e)}")
        return jsonify({"error": f"An unexpected error occurred: {str(e)}"}), 500




@app.route('/client/container/<name>', methods=['GET'])
@login_required_route
def inspect_container(name):
    # Use the container name provided in the route
    container_name = name

    try:
        # Run the 'docker inspect' command and capture the output as a JSON string
        inspect_output = subprocess.check_output(['docker', 'inspect', container_name])
        # Convert the JSON string to a Python dictionary
        inspect_data = json.loads(inspect_output.decode('utf-8'))

        # Return the JSON response
        return jsonify(inspect_data)
    except Exception as e:
        # Handle any errors and return an error response
        return jsonify({'error': str(e)}), 500



@app.route('/get_user_stats', methods=['POST'])
@login_required_route
def get_user_stats():
    username = request.form.get('username')

    if not username:
        return jsonify({"error": "Username not provided"}), 400

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



def get_hostname():
    try:
        hostname = socket.gethostname()
        return hostname
    except Exception as e:
        return f"Error: {e}"

def get_first_ip(current_username):
    dedicated_ip_file_path = f"/etc/openpanel/openpanel/core/users/{current_username}/ip.json"
    if os.path.exists(dedicated_ip_file_path):
        with open(dedicated_ip_file_path, 'r') as file:
            data = json.load(file)
            return data.get("ip", "Unknown")
    else:
        try:
            output = subprocess.check_output(["hostname", "-I"]).decode("utf-8").strip()
            ips = output.split()
            return ips[0] if ips else "Unknown"
        except subprocess.CalledProcessError:
            # If the subprocess fails, set server_ip to "Unknown"
            return "Unknown"






    try:
        result = subprocess.check_output(['hostname', '-I'], universal_newlines=True)
        ips = result.strip().split()
        if ips:
            return ips[0]
        else:
            return "No IP address found"
    except subprocess.CalledProcessError as e:
        return f"Error: {e}"



@app.route('/service/<action>/<service>', methods=['POST'])
@login_required_route
def manage_user_service(action, service):
    container_name = request.args.get('container')

    try:
        if action == 'start':
            subprocess.check_output(['docker', 'exec', container_name, 'service', service, 'start'])
            message = f'Started service {service} in container {container_name} successfully.'
        elif action == 'stop':
            subprocess.check_output(['docker', 'exec', container_name, 'service', service, 'stop'])
            message = f'Stopped service {service} in container {container_name} successfully.'
        elif action == 'restart':
            subprocess.check_output(['docker', 'exec', container_name, 'service', service, 'restart'])
            message = f'Restarted service {service} in container {container_name} successfully.'
        else:
            return jsonify({'error': 'Invalid action specified. Use start, stop, or restart.'}), 400

        return jsonify({'message': message})
    except Exception as e:
        return jsonify({'error': str(e)}), 500



@app.route('/list_services', methods=['GET'])
@login_required_route
def list_services():
    container_name = request.args.get('container_name')

    if not container_name:
        return jsonify({'error': 'Container name is required'}), 400

    try:
        # Run the command inside the container
        command = f"docker exec {container_name} service --status-all"
        result = subprocess.check_output(command, shell=True, text=True)
        
        # Process the result and split into status and service name
        services_data = [line.strip() for line in result.split('\n') if line.strip()]
        services = [{'status': 'ON' if '[ + ]' in service else 'OFF', 'name': service.replace('[ + ]', '').replace('[ - ]', '').strip()} for service in services_data]

        return jsonify({'services': services})
    except subprocess.CalledProcessError as e:
        return jsonify({'error': f'Error executing command: {e.output}'}), 500
    except Exception as e:
        return jsonify({'error': f'An unexpected error occurred: {str(e)}'}), 500


@app.route('/dns-bind/<domain>', methods=['GET'])
@login_required_route
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




@app.route('/nginx-logs/<domain>', methods=['GET'])
@login_required_route
def get_log_nginx_content(domain):
    nginx_file_path = f'/var/log/nginx/domlogs/{domain}.log'
    if os.path.exists(nginx_file_path):
        with open(nginx_file_path, 'r') as file:
            lines = file.readlines()
            last_50_lines = lines[-50:]
            nginx_content = ''.join(last_50_lines)
        return jsonify({'domain': domain, 'nginx_content': nginx_content})
    else:
        return jsonify({'error': 'Domain not found'}), 404



@app.route('/nginx-vhosts/<domain>', methods=['GET'])
@login_required_route
def get_dns_nginx_content(domain):
    nginx_file_path = f'/etc/nginx/sites-available/{domain}.conf'
    if os.path.exists(nginx_file_path):
        with open(nginx_file_path, 'r') as file:
            nginx_content = file.read()
        return jsonify({'domain': domain, 'nginx_content': nginx_content})
    else:
        return jsonify({'error': 'Domain not found'}), 404


@app.route('/save-vhosts/<domain>', methods=['POST'])
@login_required_route
def save_nginx_vhosts(domain):
    try:
        new_content = request.json['new_content']
        with open(f'/etc/nginx/sites-available/{domain}.conf', 'w') as file:
            file.write(new_content)
        os.system('docker exec nginx bash -c "nginx -t && nginx -s reload"')
        return jsonify({'success': True, 'message': 'Nginx configuration file saved successfully'})
    except Exception as e:
        return jsonify({'success': False, 'error': str(e)}), 500



@app.route('/user_activity/<username>', methods=['GET'])
@login_required_route
def get_user_activity(username):
    log_file_path = f'/etc/openpanel/openpanel/core/users/{username}/activity.log'
    try:
        with open(log_file_path, 'r') as file:
            content = file.readlines()
            reversed_content = reversed(content)
            reversed_content_str = ''.join(reversed_content)
            return jsonify({'user_activity': reversed_content_str})

    except FileNotFoundError:
        return jsonify({'error': f'Activity log for user {username} not found'}), 404




def get_directory_size(path):
    total_size = 0
    with os.scandir(path) as it:
        for entry in it:
            if entry.is_file():
                total_size += entry.stat().st_size
            elif entry.is_dir():
                total_size += get_directory_size(entry.path)
    return total_size

@app.route('/backup_info/<username>')
@login_required_route
def backup_info(username):
    backups_path = f'/backup/{username}/'

    if not os.path.exists(backups_path):
        return jsonify({'error': f'User {username} does not exist or has no backups.'})

    timestamp_directories = [d for d in os.listdir(backups_path) if os.path.isdir(os.path.join(backups_path, d))]

    backup_info = {
        'username': username,
        'total_backups': len(timestamp_directories),
        'total_disk_size': 0,
        'backups': {}
    }

    for timestamp_dir in timestamp_directories:
        timestamp_dir_path = os.path.join(backups_path, timestamp_dir)
        contents = os.listdir(timestamp_dir_path)

        file_sizes = {file: os.path.getsize(os.path.join(timestamp_dir_path, file)) for file in contents}

        # Calculate the size of the entire directory
        directory_size = get_directory_size(timestamp_dir_path)

        # Update the total disk size
        backup_info['total_disk_size'] += directory_size

        backup_info['backups'][timestamp_dir] = {
            'contents': contents,
            'file_sizes': file_sizes,
            'directory_size': directory_size
        }

    return jsonify(backup_info)

@app.route('/user/new', methods=['POST'])
@login_required_route
def create_user():
    admin_email = request.form.get('admin_email')
    admin_username = request.form.get('admin_username').lower()
    admin_password = request.form.get('admin_password')
    plan_name = request.form.get('plan_name')

    command = f"opencli user-add {admin_username} '{admin_password}' {admin_email} {plan_name}"

    # Check if 'debug' is in the form data
    if 'debug' in request.form:
        command += " --debug"

    try:
        response = subprocess.check_output(command, shell=True, text=True)
        formatted_response = {'message': response}
        return jsonify({'success': True, 'response': formatted_response})

    except subprocess.CalledProcessError as e:
        error_message = e.output if e.output else str(e)
        return jsonify({'success': False, 'error': error_message})



# HELPER FOR GEIP API
def is_geoip_installed():
    try:
        # apt-get install geoip-bin
        subprocess.check_output(['which', 'geoiplookup'])
        return True
    except subprocess.CalledProcessError:
        return False

def get_country_info(server_ip):
    if is_geoip_installed():
        try:
            ip_lookup_result = subprocess.check_output(['geoiplookup', server_ip]).decode('utf-8')
            if "GeoIP Country Edition: IP Address not found" in ip_lookup_result:
                return None, None
            else:
                country_info = ip_lookup_result.split(': ')[-1].strip()
                return tuple(country_info.split(', '))
        except subprocess.CalledProcessError as e:
            print(f"Error executing geoiplookup: {e}")
            return None, None
    else:
        try:
            response = requests.get(f"https://api.openpanel.co/countrycode/{server_ip}")
            response.raise_for_status()
            data = response.json()
            return data.get("country"), None  # Assuming you only need the country code
        except requests.RequestException as e:
            print(f"Error fetching country information from remote service: {e}")
            return None, None




@app.route('/get_custom_message_for_user/<username>', methods=['GET', 'POST'])
@login_required_route
def manage_custom_message(username):
    custom_message_file_path = f"/etc/openpanel/openpanel/core/users/{username}/custom_message.html"
    
    if request.method == 'GET':
        if os.path.exists(custom_message_file_path):
            with open(custom_message_file_path, 'r') as file:
                custom_message = file.read()
            return jsonify({'custom_message': custom_message}), 200
        else:
            return jsonify({'message': 'No custom message found'}), 404

    elif request.method == 'POST':
        if request.is_json:
            data = request.get_json()
            custom_message = data.get('custom_message')
            if custom_message:
                os.makedirs(os.path.dirname(custom_message_file_path), exist_ok=True)
                with open(custom_message_file_path, 'w') as file:
                    file.write(custom_message)
                return jsonify({'message': 'Custom message saved successfully'}), 200
            else:
                return jsonify({'error': 'No custom message provided'}), 400
        else:
            return jsonify({'error': 'Invalid content type. Expected JSON.'}), 400


# USERS ROUTE
@app.route('/users', defaults={'username': None}, methods=['GET', 'POST'])
@app.route('/users/', defaults={'username': None}, methods=['GET', 'POST'])
@app.route('/users/<username>', methods=['GET'])
@login_required_route
def users(username):
    current_route = request.path
    
    if username:
        # If a username is provided, display a specific user
        user = get_userdata_by_username(username)
        if user:
            server_ip = get_first_ip(username)
            country_code, country_name = get_country_info(server_ip)
            server_name = get_hostname()
            return render_template('single_user.html', title=f'User: {username}', server_ip=server_ip, server_name=server_name, user=user, app=app, current_route=current_route, country_code=country_code, country_name=country_name, gravatar_url=gravatar_url, get_user_websites=get_user_websites, get_hosting_plan_name_by_id=get_hosting_plan_name_by_id)
        else:
            return "User not found"
    else:
        # If no username is provided, display a list of all users
        users_list = get_all_users()
        hosting_plans = get_all_plans()
        users = users_list if users_list else []
        plans = hosting_plans if hosting_plans else []
        messages = get_flashed_messages()
        return render_template('users.html', title='Users', messages=messages, gravatar_url=gravatar_url, users=users, plans=plans, app=app, current_route=current_route)




# returns a list of users with dedicated ip addresses
@app.route('/json/ips', methods=['GET'])
@login_required_route
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
def disk_inodes_route(username):
    if not username:
        abort(401, "User not authenticated")

    # Determine Docker storage driver (overlay2 or devicemapper)
    storage_query_command = "docker info --format '{{.Driver}}'"
    storage_driver = subprocess.check_output(storage_query_command, shell=True, text=True).strip()
    # Construct the command based on the storage driver
    if storage_driver == "overlay2":
        # no quotas support with overlay2, so show the entire / disk information
        full_path = "/var/lib/docker/overlay2/"
    elif storage_driver == "devicemapper":
        try:
            path_query_command = f"docker inspect --format='{{{{.GraphDriver.Data.DeviceName}}}}' {username}"
            device_name = subprocess.check_output(path_query_command, shell=True, text=True)
            path = device_name.split('-')[-1].strip()
            full_path = f"/var/lib/docker/devicemapper/mnt/{path}" # -v /:/hostfs:ro \
        except subprocess.CalledProcessError as e:
            return jsonify({"error": f"Error getting path: {e}"}), 500
    else:
        return jsonify({"error": "Unsupported storage driver"}), 500


    try:
        # Assuming /home/username is the home path
        home_path = f"/home/{username}"

        combined_command = f"df -B1 --output=itotal,iused,size,used,target {full_path} {home_path} | tail -n 2"

        combined_output = subprocess.check_output(combined_command, shell=True, text=True) 
        df_dict = parse_df_output(combined_output)
        
        if df_dict is not None:
            formatted_output = {
                "home_itotal": df_dict.get("itotal_last", "N/A"),
                "home_iused": df_dict.get("iused_last", "N/A"),
                "home_total": df_dict.get("size_last", "N/A"),
                "home_used": df_dict.get("used_last", "N/A"),
                "home_path": df_dict.get("target_last", "N/A"),
                "devicemapper_itotal": df_dict.get("itotal_penultimate", "N/A"),
                "devicemapper_iused": df_dict.get("iused_penultimate", "N/A"),
                "devicemapper_total": df_dict.get("size_penultimate", "N/A"),
                "devicemapper_used": df_dict.get("used_penultimate", "N/A"),
                "devicemapper": df_dict.get("target_penultimate", "N/A")
            }
            return jsonify(formatted_output)
        else:
            return jsonify({"error": f"Unexpected format of df output. {combined_command}"}), 500

    except subprocess.CalledProcessError as e:
        return jsonify({"error": f"Error running df command: {e}"}), 500



####################################################
# Docker stats

@app.route('/docker_stats/<username>')
@login_required_route
def docker_stats(username):
    if not username:
        abort(401,"User not authenticated")

    conn = connect_to_database()

    container_stats = get_container_stats(username)

    conn.close()

    return jsonify({
        'container_stats': container_stats,
    })




def get_container_stats(container_name):
    try:
        client = docker.from_env()
        container = client.containers.get(container_name)
        container_stats = container.stats(stream=False)

        cpu_percent = float(container_stats['cpu_stats']['cpu_usage']['usage_in_usermode']) / float(container_stats['cpu_stats']['system_cpu_usage']) * 100.0
        mem_usage = container_stats['memory_stats']['usage']
        mem_limit = container_stats['memory_stats']['limit']
        mem_percent = (float(mem_usage) / float(mem_limit)) * 100.0

        return {
            'CPU %': f'{cpu_percent:.2f}%',
            'Memory Usage': f'{float(mem_usage) / (1024 ** 2):.2f} MB',
            'Memory Limit': f'{float(mem_limit) / (1024 ** 2):.2f} MB',
            'Memory %': f'{mem_percent:.2f}%'
        }

    except DockerException as e:
        return jsonify({"status": "error", "message": "Docker is unavailable. Please check the Docker service."}), 503
    
    except docker.errors.NotFound:
        return {'error': f"Container '{container_name}' not found"}

    except Exception as e:
        return {'error': f"An error occurred: {e}"}

# suspend, unsuspend, edit,  delete
@app.route('/user/<action>/<username>', methods=['GET', 'POST'])
@login_required_route
def manage_user(action, username):
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
                return jsonify({"message": success_message})
            else:
                flash(error_message)
                return jsonify({"message": error_message})
        except subprocess.CalledProcessError as e:
            return jsonify({"message": f"Error executing command for user '{username}': {e.output}"})

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
        new_plan_name = request.form.get('plan-type')
        new_password = request.form.get('new_password')
        new_ip = request.form.get('new_ip')
        # Get user data
        user = get_userdata_by_username(username)
        if user:
            old_plan_id = user["plan_id"]
            plan_data = get_plan_by_id(old_plan_id)
            old_plan_name = plan_data["name"]
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
                    run_command = f"opencli user-change_plan {username} {new_plan_name}"
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
                return jsonify({"message": success_message})
            else:
                flash(error_message)
                return jsonify({"message": error_message})
        except subprocess.CalledProcessError as e:
            return jsonify({"message": f"Error executing command for user '{username}': {e.output}"})
    else:
        flash("Invalid user action, valid options are: edit, suspend, unsuspend, delete")






















