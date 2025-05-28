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
# Last Modified: 18.06.2024
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




# import python modules
import os
import yaml
import glob
import re
import json
from flask import Flask, Response, render_template, request, g, jsonify, session, redirect, url_for, flash
import subprocess
import datetime
import time
from collections import OrderedDict
import psutil
from subprocess import run, Popen, PIPE

# import our modules
from app import app, cache, admin_required_route, login_required_route, load_openpanel_config
from modules.helpers import get_all_users, get_counts_from_db, get_all_plans, get_docker_contexts, get_all_emails, get_containers_count_from_all_contexts


# HOME PAGE
@app.route('/')
@app.route('/dashboard')
@admin_required_route
#######@cache.memoize(timeout=5)  # for widgets!
def dashboard():
    current_route = request.path
    
    openpanel_news_template_path = os.path.join(app.template_folder, 'dashboard/openpanel_news.html')
    openpanel_news = os.path.exists(openpanel_news_template_path)

    server_data_template_path = os.path.join(app.template_folder, 'dashboard/server_data.html')
    server_data = os.path.exists(server_data_template_path)


    try_enterprise_template_path = os.path.join(app.template_folder, 'dashboard/try_enterprise.html')
    try_enterprise = os.path.exists(try_enterprise_template_path)

    try:
        counts = get_counts_from_db()
        containers = get_containers_count_from_all_contexts()

        # read config file
        config_file_path = '/etc/openpanel/openpanel/conf/openpanel.config'
        config_data = load_openpanel_config(config_file_path)
        force_domain = config_data.get('DEFAULT', {}).get('force_domain', '')

        # a tuple with 4 values
        user_count = counts[0] if counts and len(counts) >= 1 else 0
        plan_count = counts[1] if counts and len(counts) >= 2 else 0
        site_count = counts[2] if counts and len(counts) >= 3 else 0
        domain_count = counts[3] if counts and len(counts) >= 4 else 0

        docker_context_count = get_docker_contexts()
        emails_and_quotas = get_all_emails()
        mail_count = len(emails_and_quotas)

        if request.args.get('output') == 'json':
            return jsonify({
                'force_domain': force_domain,
                'user_count': user_count,
                'plan_count': plan_count,
                'site_count': site_count,
                'running_containers': containers,
                'domain_count': domain_count,
                'mail_count': mail_count,
                'server_count': docker_context_count,
                'server_data': server_data,
                'openpanel_news': openpanel_news,
                'try_enterprise': try_enterprise,
            })

        return render_template('dashboard/dashboard.html',
                                force_domain=force_domain,
                                title='Dashboard',
                                user_count=user_count,
                                plan_count=plan_count,
                                site_count=site_count,
                                running_containers=containers,
                                domain_count=domain_count,
                                mail_count=mail_count,
                                server_count=docker_context_count,
                                app=app,
                                current_route=current_route,
                                server_data=server_data,
                                openpanel_news=openpanel_news,
                                try_enterprise=try_enterprise)

    except Exception as e:
        print(f"An error occurred: {e}")
        # default values to ensure all variables are set
        error_data = {
            'user_count': 0,
            'plan_count': 0,
            'site_count': 0,
            'domain_count': 0,
            'mail_count': 0,
            'server_count': 1,
            'running_containers': 0,
            'openpanel_news': False,
            'server_data': False,
            'try_enterprise': False,
        }

        if request.args.get('output') == 'json':
            return jsonify(error_data)

        return render_template('dashboard/dashboard.html',
                               title='Dashboard',
                               app=app,
                               current_route=current_route,
                               **error_data)


@app.route('/dashboard/dismiss/<template_name>', methods=['POST'])
@admin_required_route
def dismiss_dashboard_widget(template_name):
    base_template_path = os.path.join(app.template_folder, 'dashboard')

    valid_templates = [
        'openpanel_news',
        'cpu_per_core_usage',
        'usage_graphs',
        'try_enterprise',
        'server_data'
    ]

    if template_name in valid_templates:
        template_path = os.path.join(base_template_path, f'{template_name}.html')
        hidden_template_path = os.path.join(base_template_path, f'HIDE_{template_name}.html')

        # Rename the template file if it exists
        if os.path.exists(template_path):
            os.rename(template_path, hidden_template_path)

    return redirect(url_for('dashboard'))




@cache.memoize(timeout=300)
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
    allowed_services = [service['real_name'] for service in services]
    return allowed_services


@app.route('/service/<action>/<service_name>', methods=['GET', 'POST'])
@admin_required_route
def manage_service(action, service_name):
    # Available services and actions
    allowed_services = load_allowed_services()
    service_actions = ['start', 'restart', 'stop']

    # Validate action and service name
    if action not in service_actions:
        flash(f'Invalid action: {action}', 'error')
        return redirect(url_for('dashboard'))

    if service_name not in allowed_services:
        flash(f'Invalid service: {service_name}', 'error')
        return redirect(url_for('dashboard'))

    # Dictionary to map services to their respective Docker container names
    docker_services = {
        'openpanel_mysql': 'openpanel_mysql',
        'caddy': 'caddy',
        'openpanel': 'openpanel',
        'openpanel_dns': 'bind9',
        'openadmin_mailserver': 'openadmin_mailserver',
        'openadmin_ftp': 'openadmin_ftp',
        'openadmin_roundcube': 'roundcube'
    }

    try:
        if service_name in docker_services:
            if service_name == 'openadmin_mailserver' or service_name == 'openadmin_roundcube': 
                working_directory = '/usr/local/mail/openmail'
                if not os.path.exists(working_directory):
                    flash(f'Error: Mail server is not installed! Emails are only available on OpenPanel Enterprise version and can be enabled from Emails page.', 'error')
                    return redirect(url_for('dashboard'))

            if service_name == 'openadmin_mailserver':
                docker_services[service_name] = "mailserver" 
            else:
                working_directory = '/root'



            if action == 'start':
                compose_command = ['docker', 'compose', 'up', '-d', docker_services[service_name]]
            elif action == 'stop':
                compose_command = ['docker', 'compose', 'down', docker_services[service_name]]
            elif action == 'restart':
                stop_command = ['docker', 'compose', 'down', docker_services[service_name]]
                start_command = ['docker', 'compose', 'up', '-d', docker_services[service_name]]
                
                subprocess.run(stop_command, check=True, cwd=working_directory)
                subprocess.run(start_command, check=True, cwd=working_directory)
                flash(f'{service_name.capitalize()} restarted successfully', 'success')
                return redirect(url_for('dashboard'))
            else:
                raise ValueError(f"Invalid action: {action}")
            
            result = subprocess.run(compose_command, check=True, cwd=working_directory, stdout=subprocess.PIPE, stderr=subprocess.PIPE)
            if result.returncode == 0:
                flash(f'{service_name.capitalize()} {action}ed successfully', 'success')
            else:
                flash(f'Error {action}ing {service_name}: {result.stderr.decode()}', 'error')
        else:
            compose_command = ['service', service_name, action]
            result = subprocess.run(compose_command, check=True, cwd='/root', stdout=subprocess.PIPE, stderr=subprocess.PIPE)
            if result.returncode == 0:
                flash(f'{service_name.capitalize()} {action}ed successfully', 'success')
            else:
                flash(f'Error {action}ing {service_name}: {result.stderr.decode()}', 'error')
                
    except subprocess.CalledProcessError as e:
        flash(f'Error executing command: {e.stderr.decode()}', 'error')
    except ValueError as e:
        flash(str(e), 'error')
    except Exception as e:
        flash(f'Unexpected error: {str(e)}', 'error')

    return redirect(url_for('dashboard'))




# data

@cache.memoize(timeout=360)
def get_disk_usage_snapshot():
    partitions = psutil.disk_partitions(all=False)
    sshfs_partitions = [partition for partition in psutil.disk_partitions(all=True) if 'sshfs' in partition.fstype]
    filtered_partitions = [partition for partition in partitions if not partition.mountpoint.startswith('/snap') and not partition.mountpoint.startswith('/boot') and not partition.mountpoint.startswith('/etc/bind')]
    filtered_partitions.extend(sshfs_partitions)

    seen_mountpoints = set()
    unique_partitions = []
    for partition in filtered_partitions:
        if partition.mountpoint not in seen_mountpoints:
            seen_mountpoints.add(partition.mountpoint)
            unique_partitions.append(partition)

    disk_usage_info = []
    for partition in unique_partitions:
        try:
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
        except Exception as e:
            disk_usage_info.append({
                'device': partition.device,
                'mountpoint': partition.mountpoint,
                'fstype': partition.fstype,
                'error': str(e)
            })

    return disk_usage_info

def get_ram_usage_snapshot():
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

    return {"ram_info": ram_info, "human_readable_info": human_readable_info}

def get_load_usage_snapshot():
    load_avg = psutil.getloadavg()
    return {"load1min": load_avg[0], "load5min": load_avg[1], "load15min": load_avg[2]}

def get_cpu_usage_snapshot():
    cpu_percent_per_core = psutil.cpu_percent(interval=1, percpu=True)
    return {f"core_{i}": percent for i, percent in enumerate(cpu_percent_per_core)}


@app.route('/sse/usage', methods=['GET'])
@login_required_route
def combined_system_usage():
    def generate_combined_usage():
        while True:
            try:
                usage_data = {
                    "cpu": get_cpu_usage_snapshot(),
                    "memory": get_ram_usage_snapshot(),
                    "load": get_load_usage_snapshot(),
                    "disk": get_disk_usage_snapshot(),
                }
                yield f"data: {json.dumps(usage_data)}\n\n"
                time.sleep(30)  # Update interval
            except Exception as e:
                yield f"event: error\ndata: {json.dumps({'error': str(e)})}\n\n"
                time.sleep(60)

    return Response(generate_combined_usage(), mimetype='text/event-stream')


@app.route('/json/<resource>', methods=['GET'])
@admin_required_route
def system_resource_usage(resource):
    if resource == "disk":
        return jsonify(get_disk_usage_snapshot())
    elif resource == "memory":
        return jsonify(get_ram_usage_snapshot())
    elif resource == "load":
        return jsonify(get_load_usage_snapshot())
    elif resource == "cpu":
        return jsonify(get_cpu_usage_snapshot())
    else:
        return jsonify({"error": f"Invalid resource '{resource}' requested"}), 400


@app.route('/json/services-status')
@admin_required_route
@cache.memoize(timeout=60)
def services_status():
    config_path = '/etc/openpanel/openadmin/config/services.json'

    try:
        with open(config_path, 'r') as f:
            services_config = json.load(f)
    except Exception as e:
        return jsonify({"error": f"Unable to read services.json file: {str(e)}"}), 500

    status_data = []  # Ensure this is initialized as a list

    # Function to check Docker container status
    def check_docker_status(container_name):
        docker_cmd = 'docker ps --format "{{.Names}}"'
        docker_result = subprocess.run(docker_cmd, shell=True, stdout=subprocess.PIPE, stderr=subprocess.PIPE, text=True)
        container_names = docker_result.stdout.splitlines()
        return 'Active' if container_name in container_names else 'Inactive'


    # Check status for services
    for service in services_config:
        if service.get('on_dashboard'):
            try:
                if service['type'] == 'docker':
                    status = check_docker_status(service['real_name'])
                else:
                    # Check status using systemctl for system services
                    cmd = f'systemctl is-active {service["real_name"]}.service'
                    result = run(cmd, shell=True, stdout=PIPE, stderr=PIPE, text=True)
                    status = 'Active' if result.returncode == 0 else 'Inactive'


                # if service is Inactive, those that are not started on server install need special status
                if status == 'Inactive':
                    if service['real_name'] == 'caddy': # if vhost files exist
                        user_dirs = glob.glob('/etc/openpanel/openpanel/core/users/*/')
                        if not any(os.path.isfile(os.path.join(user_dir, 'domains')) for user_dir in user_dirs):
                            status = 'NotStarted'
                    elif service['real_name'] == 'openpanel':
                        users_dir = '/etc/openpanel/openpanel/core/users' # dirs are created for each user
                        if not os.path.exists(users_dir) or not any(os.path.isdir(os.path.join(users_dir, d)) for d in os.listdir(users_dir)):
                            status = 'NotStarted'
                    elif service['real_name'] == 'openpanel_dns':
                        if not os.path.exists('/etc/bind/zones/'): # zones dir is created when adding 1st domain
                            status = 'NotStarted'
                    elif service['real_name'] in ['openpanel']:
                        # TODO
                        pass

            except Exception as e:
                status = 'Error: ' + str(e)


            service_with_status = service.copy()
            service_with_status['status'] = status
            status_data.append(service_with_status)

    return jsonify(status_data)




@app.route('/json/combined_activity', methods=['GET'])
@admin_required_route
@cache.memoize(timeout=1800)
def combined_activity_logs():
    combined_logs = []
    logs_directory = "/etc/openpanel/openpanel/core/users"
    
    # Get list of user directories, excluding 'repquota', and sorted by modification time (newest first)
    user_dirs = [
        os.path.join(logs_directory, username) 
        for username in os.listdir(logs_directory) 
        if os.path.isdir(os.path.join(logs_directory, username)) and username != 'repquota'
    ]
    
    # Sort directories by modification time, descending (newest first)
    user_dirs.sort(key=lambda x: os.path.getmtime(x), reverse=True)

    # Limit to the 10 most recent directories
    user_dirs = user_dirs[:10]

    # Loop through the most recent directories
    for user_dir_path in user_dirs:
        user_log_path = os.path.join(user_dir_path, 'activity.log')
        
        # Read the activity log if it exists
        try:
            with open(user_log_path, 'r') as user_log:
                # Read the last 20 lines from the log
                logs = user_log.readlines()[-20:]
                combined_logs.extend([log.strip() for log in logs if log.strip()])  # Remove empty lines
        except FileNotFoundError:
            # If the log file is missing, silently skip it
            pass
        except PermissionError:
            # Handle permission errors
            continue
        except Exception as e:
            # Log any other unexpected errors (e.g., file corruption)
            print(f"Error reading {user_log_path}: {e}")
            continue

    # Sort all logs by timestamp (make sure get_timestamp_from_log function works as expected)
    combined_logs.sort(key=lambda log: get_timestamp_from_log(log), reverse=True)
    
    # Only keep the latest 20 logs
    combined_logs = combined_logs[:20]

    # Return the combined logs in a JSON response
    response_data = {'combined_logs': combined_logs}
    return jsonify(response_data)

def get_timestamp_from_log(log_entry):
    timestamp_str = log_entry.split(' ', 1)[0]
    timestamp_object = datetime.datetime.strptime(timestamp_str, '%Y-%m-%d')

    return timestamp_object

# todo: move to separate module
@app.route('/server/processes')
@admin_required_route
def server_processes():
    current_route = request.path
    sort_by = request.args.get('sort', 'cpu')  # Default sort by CPU
    processes = get_all_processes(sort_by)
    if request.args.get('output') == 'json':
        return jsonify(processes)
    return render_template('system/processes.html', title='Process Manager', current_route=current_route, processes=processes, sort_by=sort_by)

sort_criteria = {
    'cpu': ('cpu_percent', True),
    '-cpu': ('cpu_percent', False),
    'memory': ('memory_percent', True),
    '-memory': ('memory_percent', False),
    'priority': ('priority', True),
    '-priority': ('priority', False),
    'name': ('name', True),
    '-name': ('name', False),
    'owner': ('owner', True),
    '-owner': ('owner', False),
    'command': ('command', True),
    'pid': ('pid', False),
    '-pid': ('pid', True)
}


def get_all_processes(sort_by):
    # Fetch all processes
    process_list = []
    
    for proc in psutil.process_iter(['pid', 'name', 'cpu_percent', 'memory_info', 'nice', 'username', 'cmdline']):
        try:
            pid = proc.info['pid']
            name = proc.info['name']
            cpu_percent = proc.info['cpu_percent']
            memory_percent = proc.info['memory_info'].rss / psutil.virtual_memory().total * 100
            priority = proc.info['nice']
            owner = proc.info['username']
            command = ' '.join(proc.info.get('cmdline') or []) # for zombie process return empty
            
            process_list.append({
                'pid': pid,
                'name': name,
                'cpu_percent': cpu_percent,
                'memory_percent': memory_percent,
                'priority': priority,
                'owner': owner,
                'command': command
            })
        except (psutil.NoSuchProcess, psutil.AccessDenied, psutil.ZombieProcess):
            pass  # zombies
    
    # Sort by query parameter
    if sort_by in sort_criteria:
        key, reverse = sort_criteria[sort_by]
    else:
        key = 'cpy'
        reverse = True

    process_list.sort(key=lambda x: x[key], reverse=reverse)
 
    return process_list





def generate_strace_output(pid):
    process = subprocess.Popen(['strace', '-p', str(pid)], stdout=subprocess.PIPE, stderr=subprocess.STDOUT, text=True, bufsize=1, close_fds=True)

    for line in iter(process.stdout.readline, ''):
        yield line.rstrip() + '\n'

import signal
@app.route('/server/processes/<int:pid>/<action>', methods=['GET'])
@admin_required_route
def perform_action(pid, action):
    if action not in ['kill', 'strace']:
        flash(f'Invalid action: {action}', 'error')
    if action == 'strace':
        if request.args.get('output') == 'stream':
            return Response(generate_strace_output(pid), content_type='text/plain;charset=utf-8')
        else:
            current_route = request.path
            return render_template('system/strace.html', title=f'Strace {pid}', pid=pid, current_route=current_route)
    elif action == 'kill':
        try:
            os.kill(pid, signal.SIGTERM)
            flash(f'Process with PID {pid} killed successfully', 'success')
        except Exception as e:
            flash(f'Error killing process: {str(e)}', 'error')
        except Exception as e:
            flash(f'Error killing pid!', 'error')
    return redirect(url_for('server_processes'))
    
