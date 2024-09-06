################################################################################
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
from app import app, login_required_route, load_openpanel_config

CONFIG_FILE = '/etc/openpanel/openadmin/config/services.json'

def get_service_status(service):
    service_name = service.get('real_name')
    service_type = service.get('type')

    if service_type == 'docker':
        try:
            result = subprocess.run(
                ['docker', 'inspect', '-f', '{{.State.Running}}', service_name],
                stdout=subprocess.PIPE, stderr=subprocess.PIPE, text=True
            )
            return result.stdout.strip() == 'true'
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

def control_service(service, action):
    service_name = service.get('real_name')
    service_type = service.get('type')

    if service_type == 'docker':
        command = ['docker', action, service_name]
    else:  # system service
        command = ['systemctl', action, service_name]

    try:
        result = subprocess.run(command, stdout=subprocess.PIPE, stderr=subprocess.PIPE, text=True)
        return result.returncode == 0, result.stdout if result.returncode == 0 else result.stderr
    except Exception as e:
        return False, str(e)



def get_service_version(service):
    service_name = service.get('real_name')
    service_type = service.get('type')

    try:
        if service_name == 'admin' or service_name == 'openpanel':
            version_file = '/usr/local/panel/version'
            with open(version_file, 'r') as f:
                version = f.read().strip()
                version = re.search(r'\d+(\.\d+)+', version).group()  # Extract numeric parts
        elif service_name == 'ufw':
            try:
                result = subprocess.run(
                    ['ufw', '--version'],
                    stderr=subprocess.PIPE,
                    stdout=subprocess.PIPE,
                    text=True
                )
                output = result.stderr if result.stderr else result.stdout
                version = output.strip().split()[1]
                version = re.search(r'\d+(\.\d+)+', version).group()
            except IndexError:
                version = 'Error: Unable to extract version'
            except Exception as e:
                version = f'Error: {str(e)}'

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
                f"sh -c \"grep 'Version' /var/www/html/index.php | awk -F' ' '{{print $2}}' | tr -d '|'\""
            )
            
            result = subprocess.run(
                command, shell=True, stdout=subprocess.PIPE, stderr=subprocess.PIPE, text=True
            )
            
            version = result.stdout.strip()
        elif service_name == 'admin':
            version = None
        elif service_name == 'openpanel_mysql':
            result = subprocess.run(
                ['docker', 'exec', service_name, 'mysql', '--version'],
                stdout=subprocess.PIPE, stderr=subprocess.PIPE, text=True
            )
            version = result.stdout.strip().split()[2].split(',')[0]
            version = re.search(r'\d+(\.\d+)+', version).group()  # Extract numeric parts
        elif service_name == 'nginx':
            result = subprocess.run(
                ['docker', 'exec', 'nginx', 'nginx', '-v'],
                stdout=subprocess.PIPE, stderr=subprocess.PIPE, text=True
            )

            # Extract version from stderr
            version_info = result.stderr.strip()
            version_match = re.search(r'nginx/(\d+\.\d+\.\d+)', version_info)

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

@app.route('/services/status', methods=['GET'])
@login_required_route
def services_status_page():
    services = load_monitored_services()
    statuses = {}
    
    docker_client = docker.from_env()
    
    for service in services:
        service_display_name = service.get('name')
        real_name = service.get('real_name')
        service_type = service.get('type')
        
        # Fetch status and version
        status = get_service_status(service)
        version = get_service_version(service)
        
        # Initialize port variable
        port = None
        
        if real_name == 'openpanel':
            config_file_path = '/etc/openpanel/openpanel/conf/openpanel.config'
            config_data = load_openpanel_config(config_file_path)
            port = config_data.get('DEFAULT', {}).get('port', '2083')
        elif service_type == 'docker':
            try:
                container = docker_client.containers.get(real_name)
                port_info = container.attrs['NetworkSettings']['Ports']
                ports = {}
                for port_name, port_data in port_info.items():
                    if port_data:
                        # Extract unique host ports
                        unique_ports = {p['HostPort'] for p in port_data if p.get('HostPort')}
                        # Convert set to list and get the first item (if any)
                        ports[port_name] = list(unique_ports)[0] if unique_ports else 'No ports exposed'

                port = next(iter(ports.values()), 'No ports exposed')
                
                # Output the ports dictionary
                ########port = ports if ports else 'No ports exposed'
            except Exception as e:
                ###3port = f'Error: {str(e)}'
                port = None
        elif real_name == 'admin':
            port = "2087"
        elif real_name == 'docker' or real_name == 'csf' or real_name == 'ufw':
            port = None
        else:
            port = None

        # Update statuses dictionary
        statuses[service_display_name] = {
            'real_name': real_name,
            'type': service_type,
            'status': status,
            'version': version,
            'port': port
        }

    # Check for the 'json' flag in the query parameters
    if 'json' in request.args:
        return jsonify(statuses)

    return render_template('services/status.html', statuses=statuses)

@app.route('/services/control', methods=['POST'])
@login_required_route
def services_control():
    data = request.json
    real_name = data.get('real_name')
    action = data.get('action')

    if action not in ['start', 'stop', 'restart']:
        return jsonify({'error': 'Invalid action'}), 400

    service = find_service_by_real_name(real_name)
    if not service:
        return jsonify({'error': 'Service not found'}), 404

    success, message = control_service(service, action)
    if success:
        return jsonify({'status': 'success', 'message': message})
    else:
        return jsonify({'status': 'failure', 'message': message}), 500
