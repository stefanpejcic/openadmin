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
# Created: 23.06.2024
# Last Modified: 05.09.2024
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
import subprocess
from flask import Flask, Response, abort, render_template, request, send_file, g, jsonify, session, url_for, flash, redirect, get_flashed_messages
from app import app, cache, admin_required_route, load_openpanel_config, get_openpanel_version
import yaml
from modules.helpers import get_user_and_plan_count
CONFIG_FILE = '/etc/openpanel/openadmin/config/services.json'



@app.route('/services/edit', methods=['GET', 'POST'])
def edit_services():
    data = {}

    if request.method == 'POST':
        try:
            new_data = request.form.get('data', '').strip()

            try:
                parsed_data = json.loads(new_data)
            except json.JSONDecodeError as e:
                flash(f"Invalid JSON data: {e.msg}", 'error')
                return redirect(url_for('edit_services'))

            # Save the data to the config file
            with open(CONFIG_FILE, 'w') as file:
                json.dump(parsed_data, file, indent=4)

            flash('Config file updated successfully.', 'success')
        except Exception as e:
            flash(f'Error saving the file: {str(e)}. Please edit via terminal: /etc/openpanel/openadmin/config/services.json', 'error')

    if os.path.exists(CONFIG_FILE):
        with open(CONFIG_FILE, 'r') as file:
            try:
                data = json.load(file)
            except json.JSONDecodeError:
                flash('Error: Invalid JSON format in config file.', 'error')

    if 'json' in request.args:
        return jsonify(data)

    pretty_data = json.dumps(data, indent=2)
    return render_template('services/edit_services.html', data=pretty_data, title="Edit Monitored Services")




def get_service_status(service):
    service_name = service.get('real_name')
    service_type = service.get('type')

    if service_type == 'docker':
        docker_services = {
            'openpanel_mysql': 'openpanel_mysql',
            'caddy': 'caddy',
            'openpanel': 'openpanel',
            'openpanel_dns': 'openpanel_dns',
            'openadmin_mailserver': 'openadmin_mailserver',
            'openadmin_ftp': 'openadmin_ftp',
            'clamav': 'clamav',
            'openadmin_roundcube': 'openadmin_roundcube'
        }

        try:
            result = subprocess.run(
                ['docker', 'inspect', '-f', '{{.State.Running}}', service_name],
                stdout=subprocess.PIPE, stderr=subprocess.PIPE, text=True
            )
            if 'No such object' in result.stderr: # container does not exist
                counts = get_user_and_plan_count()
                user_count = counts[0] if counts and len(counts) >= 1 else 0
                if user_count == 0 and service_name in docker_services:
                        return None                   # no users and core container not initialized
                else:
                    return False                      # users exists
            else:
                return result.stdout.strip() == 'true' # container exists and we check status
        except Exception as e:
            return False
    else:  # system service
        try:
            result = subprocess.run(
                ['systemctl', 'is-active', service_name],
                stdout=subprocess.PIPE, stderr=subprocess.PIPE, text=True
            )
            return result.stdout.strip() == 'active'
        except Exception as e:
            return False

def load_monitored_services():
    try:
        with open(CONFIG_FILE, 'r') as f:
            services = json.load(f)
            return services
    except FileNotFoundError:
        return []

def find_service_by_real_name(real_name):
    services = load_monitored_services()
    for service in services:
        if service.get('real_name') == real_name:
            return service
    return None


def control_service(service_name, service_type, action):
    
    # Set default command and working directory
    command = None
    working_directory = '/root'  # Default working directory

    if service_type == 'docker':
        docker_services = {
            'openpanel_mysql': 'openpanel_mysql',
            'caddy': 'caddy',
            'openpanel': 'openpanel',
            'openpanel_dns': 'bind9',
            'openadmin_mailserver': 'openadmin_mailserver',
            'openadmin_ftp': 'openadmin_ftp',
            'clamav': 'clamav',
            'openadmin_roundcube': 'roundcube'
        }

        if service_name in docker_services:
            if service_name == 'openadmin_mailserver' or service_name == 'openadmin_roundcube': 
                working_directory = '/usr/local/mail/openmail'
                if not os.path.exists(working_directory):
                    return False, f"Error: Mail server is not installed! Emails are only available on OpenPanel Enterprise version and can be enabled from Emails page."

            if service_name == 'openadmin_mailserver':
                docker_services[service_name] = "mailserver" 

            if action == 'start':
                command = ['docker', '--context', 'default', 'compose', 'up', '-d', docker_services[service_name]]
            elif action == 'stop':
                command = ['docker', '--context', 'default', 'compose', 'down', docker_services[service_name]]
            elif action == 'restart':
                command = ['bash', '-c', f'docker --context default compose down {docker_services[service_name]} && docker --context default compose up -d {docker_services[service_name]}']
        
        else:
            command = ['docker',  '--context', 'default', action, service_name]
    else:  # system service
        command = ['systemctl', action, service_name]

    if command is None:
        return False, "No command defined for the service action."

    try:
        result = subprocess.run(command, cwd=working_directory, stdout=subprocess.PIPE, stderr=subprocess.PIPE, text=True)
        return result.returncode == 0, result.stdout if result.returncode == 0 else result.stderr
    except Exception as e:
        return False, str(e)



def get_service_version(service):
    service_name = service.get('real_name')
    service_type = service.get('type')

    try:
        if service_name == 'admin' or service_name == 'openpanel':
            version = get_openpanel_version()

        elif service_name == 'csf':
            try:
                result = subprocess.run(
                    ['csf', '-v'],
                    stderr=subprocess.PIPE,
                    stdout=subprocess.PIPE,
                    text=True
                )
                output = result.stdout if result.stdout else result.stderr
                version = re.search(r'v(\d+(\.\d+)+)', output).group(1)
            except IndexError:
                version = 'Error: Unable to extract version'
            except Exception as e:
                version = f'Error: {str(e)}'


        elif service_name == 'clamav':
            try:
                command = f"docker exec {service_name} clamscan --version"
                
                result = subprocess.run(
                    command, shell=True, stdout=subprocess.PIPE, stderr=subprocess.PIPE, text=True
                )
                
                output = result.stdout.strip()
                
                # Use regex to find the version numbers before the /
                match = re.search(r'ClamAV (\d+\.\d+\.\d+)/', output)
                if match:
                    version = match.group(1)  # Get the version number
                else:
                    version = None
            except IndexError:
                version = None

        elif service_name == 'openpanel_dns':
            try:
                command = f"docker exec {service_name} named -V"
                
                result = subprocess.run(
                    command, shell=True, stdout=subprocess.PIPE, stderr=subprocess.PIPE, text=True
                )
                
                output = result.stdout.strip()
                
                # Use regex to find the version numbers
                match = re.search(r'\b(\d+\.\d+\.\d+)', output)
                if match:
                    version = match.group(1)
                else:
                    version = None
            except IndexError:
                version = None


        elif service_name == 'openadmin_roundcube':
            command = (
                f"docker exec {service_name} "
                f"sh -c \"grep 'Version' /var/www/html/index.php | awk -F' ' '{{print $2}}' | tr -d 'Version|'\""
            )
            
            result = subprocess.run(
                command, shell=True, stdout=subprocess.PIPE, stderr=subprocess.PIPE, text=True
            )
            
            version = result.stdout.strip()
        elif service_name == 'admin':
            version = None
        elif service_name == 'netdata':
            result = subprocess.run(
                ['docker', 'exec', 'netdata', 'netdata', '-version'],
                stdout=subprocess.PIPE, stderr=subprocess.PIPE, text=True
            )
            version = result.stdout.strip().split()[2].split(',')[0]
        elif service_name == 'openpanel_mysql':
            result = subprocess.run(
                ['docker', 'exec', service_name, 'mysql', '--version'],
                stdout=subprocess.PIPE, stderr=subprocess.PIPE, text=True
            )
            version = result.stdout.strip().split()[2].split(',')[0]
            version = re.search(r'\d+(\.\d+)+', version).group()  # Extract numeric parts
        elif service_name == 'caddy':
            result = subprocess.run(
                ['docker', 'exec', 'caddy', 'caddy', 'version'],
                stdout=subprocess.PIPE, stderr=subprocess.PIPE, text=True
            )

            # Extract version from stdout (since the version appears in stdout)
            version_info = result.stdout.strip()
            version_match = re.search(r'v(\d+\.\d+\.\d+)', version_info)

            if version_match:
                version = version_match.group(1)
            else:
                version = 'Unknown'  # Handle the case where version is not found
        elif service_name == 'docker':
            result = subprocess.run(
                ['docker', '--version'],
                stdout=subprocess.PIPE, text=True
            )
            version = result.stdout.strip().split()[2].split(',')[0]
            version = re.search(r'\d+(\.\d+)+', version).group()  # Extract numeric parts
        # custom services in docker containers
        elif service_type == 'docker':
            result = subprocess.run(
                ['docker', 'exec', service_name, service_name, '-v'],
                stdout=subprocess.PIPE, stderr=subprocess.PIPE, text=True
            )
            version = result.stdout.strip().split()[2].split(',')[0]
            version = re.search(r'\d+(\.\d+)+', version).group()  # Extract numeric parts
        # custom systemd services
        elif service_type == 'system':
            try:
                # Try with --version
                result = subprocess.run(
                    [service_name, '--version'],
                    stdout=subprocess.PIPE,
                    stderr=subprocess.PIPE,
                    text=True
                )
                # Check if the command was successful
                if result.returncode == 0:
                    version = result.stdout.strip().split()[2].split(',')[0]
                else:
                    # If --version fails, try with -v
                    result = subprocess.run(
                        [service_name, '-v'],
                        stdout=subprocess.PIPE,
                        stderr=subprocess.PIPE,
                        text=True
                    )
                    # Check if the command was successful
                    if result.returncode == 0:
                        version = result.stdout.strip().split()[2].split(',')[0]
                    else:
                        raise subprocess.CalledProcessError(result.returncode, result.args)
                version = re.search(r'\d+(\.\d+)+', version).group()  # Extract numeric parts
            except subprocess.CalledProcessError:
                version = None
        else:
            version = None
    except Exception as e:
        version = None

    return version

import docker
import socket
import psutil
from dotenv import load_dotenv

@cache.memoize(timeout=600)
def get_service_ports(compose_file_path, service_name, env_file_path='/root/.env'):

    # Ensure the environment variables are loaded from the .env file
    if not os.path.isfile(env_file_path):
        return f"Error: .env file not found at '{env_file_path}'."

    try:
        load_dotenv(dotenv_path=env_file_path)
    except Exception as e:
        return f"Error loading .env file: {str(e)}"

    # Check if the docker-compose file exists
    if not os.path.isfile(compose_file_path):
        return ''
        #return f"File not found for '{service_name}'."

    try:
        with open(compose_file_path, 'r') as file:
            docker_compose = yaml.safe_load(file)
    except yaml.YAMLError as e:
        return f"Error reading Docker Compose file: {str(e)}"
    
    # Handle the services and ports
    ports = []
    services = docker_compose.get('services', {})
    service = services.get(service_name, {})

    if not service:
        return f"Service '{service_name}' not found."

    # Retrieve the port configuration for the service
    if service_name == "openadmin_ftp":
        ftp_port_range = os.getenv("FTP_PORT_RANGE")
        if ftp_port_range:
            ports.append(ftp_port_range)
        else:
            return "FTP_PORT_RANGE not defined in .env file."
    elif service_name == "openpanel":
        openpanel_port = os.getenv("PORT")
        if openpanel_port:
            ports.append(openpanel_port)
        else:
            return "PORT not defined in .env file."
    else:
        ports_list = service.get('ports', [])
        for port in ports_list:
            ports.append(port.split(':')[0])  # Get the host port

    return ports if ports else f"No ports defined for service '{service_name}'."




@app.route('/services/monitored', methods=['GET'])
@admin_required_route
@cache.memoize(timeout=300)
def get_monitored_services():
    config_file = '/etc/openpanel/openadmin/config/notifications.ini'
    services_line_prefix = 'services='
    monitored_services = []

    # Check if the config file exists
    if os.path.exists(config_file):
        with open(config_file, 'r') as file:
            # Read each line in the file
            for line in file:
                # Strip any surrounding whitespace
                line = line.strip()
                # Check if the line starts with the services key
                if line.startswith(services_line_prefix):
                    # Get the services by removing the prefix and splitting the values by commas
                    monitored_services = line[len(services_line_prefix):].split(',')
                    break
    else:
        return jsonify({'error': f'Config file {config_file} does not exist'}), 404

    return jsonify({'monitored_services': monitored_services})


@app.route('/services', methods=['GET', 'POST'])
@admin_required_route
def services_status_page():

    if request.method == 'POST':
        real_name = request.form.get('real_name')
        action = request.form.get('action')
        container = request.form.get('container')

        if action not in ['start', 'stop', 'restart']:
            flash("Invalid action, please use one of the following: 'start', 'stop' or 'restart'.", "error")

        if container not in ['system', 'docker']:
            flash("Invalid service type, please set: 'system' or 'docker'.", "error")

        success, message = control_service(real_name, container, action)
        
        if success:
            flash(f"Successfully {action}ed service '{real_name}'.", 'success')
        else:
            if real_name != 'admin':
                flash(f"Failed to {action} service '{real_name}'.", 'error')
      
        redirect_to = request.form.get('redirect')
        if redirect_to:
            return redirect(redirect_to)

    services = load_monitored_services()
    statuses = {}

    for service in services:
        service_display_name = service.get('name')
        real_name = service.get('real_name')
        service_type = service.get('type')

        # Fetch status, version and port
        status = get_service_status(service)          # no cache
        version = get_cached_service_version(service) # 10 min cache
        port = get_cached_service_ports(service)      # 10 min cache
 
        # Update statuses dictionary
        statuses[service_display_name] = {
            'real_name': real_name,
            'type': service_type,
            'status': status,
            'version': version,
            'port': port
        }

    if 'json' in request.args:
        return jsonify(statuses)

    return render_template('services/status.html', statuses=statuses, title="Services Status")




def get_service_port(service):
    real_name = service.get('real_name')
    
    # Initialize port variable
    port = None

    if real_name == 'openpanel':
        config_file_path = '/etc/openpanel/openpanel/conf/openpanel.config'
        config_data = load_openpanel_config(config_file_path)
        port = config_data.get('DEFAULT', {}).get('port', '2083')

    elif real_name == 'openpanel_dns':
        port = get_service_ports("/root/docker-compose.yml", "bind9")
        if isinstance(port, list):
            port = list(set(port))  # Remove duplicates

    elif real_name == 'caddy':
        try:
            result = subprocess.run(
                ['docker', 'inspect', 'caddy'],
                stdout=subprocess.PIPE,
                stderr=subprocess.PIPE,
                check=True,
                text=True
            )
            container_info = json.loads(result.stdout)
            ports = container_info[0].get('NetworkSettings', {}).get('Ports', {})
            port_mappings = {port: details[0]['HostPort'] for port, details in ports.items() if details}
            port = list(set(port_mappings.values()))  # Remove duplicates
        except subprocess.CalledProcessError:
            port = None

    elif real_name == 'openpanel_mysql':
        port = get_service_ports("/root/docker-compose.yml", "openpanel_mysql")

    elif real_name == 'openadmin_roundcube':
        port = get_service_ports("/usr/local/mail/openmail/compose.yml", "roundcube")

    elif real_name == 'openadmin_mailserver':
        port = get_service_ports("/usr/local/mail/openmail/compose.yml", "mailserver")

    elif real_name == 'openadmin_ftp':
        port = get_service_ports("/root/docker-compose.yml", "openadmin_ftp")
        if isinstance(port, list):
            port = list(set(port))  # Remove duplicates

    elif real_name == 'admin':
        port = "2087"

    elif real_name in ['docker', 'csf']:
        port = None

    else:
        port = None

    # Ensure port is formatted correctly
    if isinstance(port, list):
        port = sorted(set(port))  # Remove duplicates and sort
    elif port is not None:
        port = str(port)  # Convert single port to string

    return port

# cache ports for 10min
@cache.memoize(timeout=600)
def get_cached_service_ports(service):
    return get_service_port(service)


# cache versions for 10min
@cache.memoize(timeout=600)
def get_cached_service_version(service):
    return get_service_version(service)

