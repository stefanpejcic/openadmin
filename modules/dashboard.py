################################################################################
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
import re
import json
from flask import Flask, Response, render_template, request, g, jsonify, session, redirect, url_for, flash
import subprocess
import datetime
from collections import OrderedDict
import psutil
from subprocess import run, Popen, PIPE

# import our modules
from app import app, login_required_route, load_openpanel_config
from modules.helpers import get_all_users, get_user_and_plan_count, get_all_plans



# RESOURCE USAGE
@app.route('/get_server_load')
@login_required_route
def get_server_load():
    try:
        load_avg = psutil.getloadavg()
        return jsonify({
            'load1min': load_avg[0],
            'load5min': load_avg[1],
            'load15min': load_avg[2]
        })
    except Exception as e:
        return str(e)
        
# HOME PAGE
@app.route('/')
@app.route('/dashboard')
@login_required_route
def dashboard():
    current_route = request.path
    get_started_template_path = os.path.join(app.template_folder, 'dashboard/get_started.html')
    get_started_enabled = os.path.exists(get_started_template_path)

    usage_template_path = os.path.join(app.template_folder, 'dashboard/history_usage_graphs.html')
    history_usage_graphs_enabled = os.path.exists(usage_template_path)
    
    custom_dashoard_message_template_path = os.path.join(app.template_folder, 'dashboard/custom_message.html')
    custom_message_is_available = os.path.exists(custom_dashoard_message_template_path)

    cpu_per_core_usage_template_path = os.path.join(app.template_folder, 'dashboard/cpu_per_core_usage.html')
    cpu_per_core_usage_is_available = os.path.exists(cpu_per_core_usage_template_path)

    try:
        counts = get_user_and_plan_count()

        # read config file
        config_file_path = '/etc/openpanel/openpanel/conf/openpanel.config'
        config_data = load_openpanel_config(config_file_path)
        force_domain = config_data.get('DEFAULT', {}).get('force_domain', '')
        ns1 = config_data.get('DEFAULT', {}).get('ns2', '')
        ns2 = config_data.get('DEFAULT', {}).get('ns2', '')
        modsec_enabled = os.path.exists('/etc/nginx/modsec/main.conf')
        is_modsec_installed = "YES" if modsec_enabled else "NO"

        # get_user_and_plan_count is a tuple with 2 values
        user_count = counts[0] if counts and len(counts) >= 1 else 0
        plan_count = counts[1] if counts and len(counts) >= 2 else 0
        # get number of backup jobs
        backup_jobs_folder = '/etc/openpanel/openadmin/config/backups/jobs/'
        if os.path.exists(backup_jobs_folder):
            json_backup_job_files = [f for f in os.listdir(backup_jobs_folder) if f.endswith('.json')]
            backup_jobs = len(json_backup_job_files)
        else:
            backup_jobs = 0

        return render_template('dashboard/dashboard.html', is_modsec_installed=is_modsec_installed, backup_jobs=backup_jobs, force_domain=force_domain, ns1=ns1, ns2=ns2, title='Dashboard', user_count=user_count, plan_count=plan_count, app=app, current_route=current_route, get_started_enabled=get_started_enabled, history_usage_graphs_enabled=history_usage_graphs_enabled, custom_message_is_available=custom_message_is_available, cpu_per_core_usage_is_available=cpu_per_core_usage_is_available)

    except Exception as e:
        print(f"An error occurred: {e}")
        # default values
        user_count = "0"
        plan_count = "0"
        backup_jobs = "0"
        return render_template('dashboard/dashboard.html', title='Dashboard', user_count=user_count, plan_count=plan_count, app=app, current_route=current_route, backup_jobs=backup_jobs, get_started_enabled=get_started_enabled, history_usage_graphs_enabled=history_usage_graphs_enabled, custom_message_is_available=custom_message_is_available, cpu_per_core_usage_is_available=cpu_per_core_usage_is_available)



@app.route('/dashboard/dismiss/get_started', methods=['POST'])
@login_required_route
def dismiss_get_started():
    template_path = os.path.join(app.template_folder, 'dashboard/get_started.html')
    hidden_template_path = os.path.join(app.template_folder, 'dashboard/HIDE_get_started.html')
    if os.path.exists(template_path):
        os.rename(template_path, hidden_template_path)
    return redirect(url_for('dashboard'))

@app.route('/dashboard/dismiss/custom_news', methods=['POST'])
@login_required_route
def dismiss_custom_news():
    template_path = os.path.join(app.template_folder, 'dashboard/custom_message.html')
    hidden_template_path = os.path.join(app.template_folder, 'dashboard/HIDE_custom_message.html')
    if os.path.exists(template_path):
        os.rename(template_path, hidden_template_path)
    return redirect(url_for('dashboard'))


@app.route('/dashboard/dismiss/cpu_per_core_usage', methods=['POST'])
@login_required_route
def dismiss_cpu_per_core_usage():
    template_path = os.path.join(app.template_folder, 'dashboard/cpu_per_core_usage.html')
    hidden_template_path = os.path.join(app.template_folder, 'dashboard/HIDE_cpu_per_core_usage.html')
    if os.path.exists(template_path):
        os.rename(template_path, hidden_template_path)
    return redirect(url_for('dashboard'))

@app.route('/dashboard/dismiss/usage_graphs', methods=['POST'])
@login_required_route
def dismiss_history_usage_graphs():
    template_path = os.path.join(app.template_folder, 'dashboard/history_usage_graphs.html')
    hidden_template_path = os.path.join(app.template_folder, 'dashboard/HIDE_history_usage_graphs.html')
    if os.path.exists(template_path):
        os.rename(template_path, hidden_template_path)
    return redirect(url_for('dashboard'))



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


@app.route('/service/<action>/<service_name>')
@login_required_route
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
        'nginx': 'nginx',
        'openpanel': 'openpanel',
        'openpanel_dns': 'openpanel_dns',
        'openadmin_mailserver': 'openadmin_mailserver',
        'roundcube': 'openadmin_roundcube'
    }

    try:
        if service_name in docker_services:
            if service_name == 'openadmin_mailserver':
                working_directory = '/usr/local/mail/openmail'
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

# list du for dashboard page
@app.route('/json/docker-context')
@login_required_route
def docker_context():
    try:
        command = ["docker", "context", "ls", "--format", '{{json .}}']
        result = subprocess.check_output(command, text=True)
        lines = result.strip().split('\n')
        docker_contexts = []

        for line in lines:
            try:
                context_data = json.loads(line)
                docker_contexts.append(context_data)
            except json.JSONDecodeError as e:
                return jsonify({'error': 'Failed to parse JSON: ' + str(e)})

        return jsonify(docker_contexts)
    except Exception as e:
        return jsonify({'error': str(e)})



@app.route('/json/disk-usage')
@login_required_route
def disk_usage():
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

# ram usage for dashboard
@app.route('/json/ram-usage')
@login_required_route
def get_ram_usage():
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

# cpu usage for dashboard
@app.route('/json/cpu-usage', methods=['GET'])
@login_required_route
def per_core_cpu_usage():
    cpu_percent_per_core = psutil.cpu_percent(interval=1, percpu=True)

    cpu_info = {
        f"core_{i}": percent for i, percent in enumerate(cpu_percent_per_core)
    }

    return jsonify(cpu_info)



@app.route('/json/services-status')
@login_required_route
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
                    if service['real_name'] == 'nginx': # if vhost files exist
                        if not any(fname.endswith('.conf') for fname in os.listdir('/etc/nginx/sites-available/')):
                            status = 'NotStarted'
                    elif service['real_name'] == 'openpanel':
                        users_dir = '/etc/openpanel/openpanel/core/users' # dirs are created for each user
                        if not os.path.exists(users_dir) or not any(os.path.isdir(os.path.join(users_dir, d)) for d in os.listdir(users_dir)):
                            status = 'NotStarted'
                    elif service['real_name'] == 'openpanel_dns':
                        if not os.path.exists('/etc/bind/zones/'): # zones dir is created when adding 1st domain
                            status = 'NotStarted'
                    elif service['real_name'] in ['openpanel', 'certbot']:
                        # TODO
                        pass

            except Exception as e:
                status = 'Error: ' + str(e)


            service_with_status = service.copy()
            service_with_status['status'] = status
            status_data.append(service_with_status)

    return jsonify(status_data)

logs_directory = "/etc/openpanel/openpanel/core/users"

@app.route('/combined_activity_logs', methods=['GET'])
@login_required_route
def combined_activity_logs():
    combined_logs = []

    for username in os.listdir(logs_directory):
        user_dir_path = os.path.join(logs_directory, username)

        if os.path.isdir(user_dir_path):  # Check if the path is a directory
            user_log_path = os.path.join(user_dir_path, 'activity.log')

            try:
                with open(user_log_path, 'r') as user_log:
                    for log_entry in user_log:
                        if log_entry.strip():  # Check if the line is not empty
                            combined_logs.append(log_entry.strip())
            except FileNotFoundError:
                pass

    combined_logs.sort(key=lambda log: get_timestamp_from_log(log), reverse=True)
    combined_logs = combined_logs[:20]

    response_data = {'combined_logs': combined_logs}
    return jsonify(response_data)

def get_timestamp_from_log(log_entry):
    timestamp_str = log_entry.split(' ', 1)[0]
    timestamp_object = datetime.datetime.strptime(timestamp_str, '%Y-%m-%d')

    return timestamp_object


# slow, should be optimized
@app.route('/server/memory_usage')
@login_required_route
def server_memory_usage():
    return get_process_info('%mem', 'system/cpu_mem.html')

@app.route('/server/cpu_usage')
@login_required_route
def server_cpu_usage():
    return get_process_info('%cpu', 'system/cpu_mem.html')

def get_process_info(usage_type, template):
    if usage_type == '%mem':
        cmd = "ps -e -o pid,comm,%mem,cgroup --sort=-%mem --no-headers"
        title="Memory"
    elif usage_type == '%cpu':
        cmd = "ps -e -o pid,comm,%cpu,cgroup --sort=-%cpu --no-headers"
        title="CPU"
    else:
        return "Unsupported usage type", 400

    result = subprocess.run(cmd, shell=True, stdout=subprocess.PIPE, stderr=subprocess.PIPE, text=True)

    if result.returncode != 0:
        return f"Error executing command: {result.stderr}", 500

    processes = []
    for line in result.stdout.strip().split('\n'):
        parts = line.rsplit(None, 3)
        if len(parts) == 4:
            raw_pid, name, usage, cgroup = parts
            numeric_pid = re.sub(r'\D', '', raw_pid)
            processes.append({
                'pid': int(numeric_pid),
                'name': name.strip(),
                'usage': float(usage),
                'cgroup': cgroup.strip()
            })

    processes = sorted(processes, key=lambda x: x['usage'], reverse=True)

    return render_template(template, processes=processes, title=title)



def generate_strace_output(pid):
    process = subprocess.Popen(['strace', '-p', str(pid)], stdout=subprocess.PIPE, stderr=subprocess.STDOUT, text=True, bufsize=1, close_fds=True)

    for line in iter(process.stdout.readline, ''):
        yield line.rstrip() + '\n'

# kill pid
@app.route('/server/pid/<int:pid>/<action>', methods=['GET'])
@login_required_route
def perform_action(pid, action):
    if action not in ['kill', 'strace']:
        return jsonify({'error': 'Invalid action'}), 400

    try:
        os.kill(pid, 0)
    except OSError:
        return jsonify({'error': f'Process with PID {pid} not found'}), 404

    if action == 'kill':
        try:
            os.kill(pid, signal.SIGTERM)
            return jsonify({'message': f'Process with PID {pid} killed successfully'}), 200
        except Exception as e:
            return jsonify({'error': f'Error killing process: {str(e)}'}), 500
    elif action == 'strace':
        return Response(generate_strace_output(pid), content_type='text/plain;charset=utf-8')


