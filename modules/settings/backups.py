################################################################################
# Author: Stefan Pejcic
# Created: 11.07.2023
# Last Modified: 13.03.2024
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
import subprocess
import datetime
import psutil
from app import app, is_license_valid, login_required_route, load_openpanel_config, connect_to_database
import docker

from modules.helpers import get_all_users, get_user_and_plan_count, get_plan_by_id, get_all_plans, get_userdata_by_username, get_hosting_plan_name_by_id, get_user_websites, is_username_unique, gravatar_url

@app.route('/backups', methods=['GET', 'POST'])
@login_required_route
def admin_backup_settings():
    if request.method == 'POST':
        return redirect('/settings/docker')
    else:
        try:
            backup_folder = '/etc/openpanel/openadmin/config/backups/jobs/'

            # Get a list of all files in the directory
            json_files = [f for f in os.listdir(backup_folder) if f.endswith('.json')]

            # Initialize an empty list to store combined data
            combined_data = []

            # Loop through each JSON file and combine their content
            for json_file in json_files:
                file_path = os.path.join(backup_folder, json_file)
                with open(file_path, 'r') as file:
                    data = json.load(file)
                    data["id"] = os.path.splitext(json_file)[0]  # Strip ".json" extension
                    combined_data.append(data)

            return render_template('backups.html', title='Backups', backup_jobs=combined_data)
        except FileNotFoundError:
            return render_template('backups.html', title='Backups', error="No backup jobs")
        except json.JSONDecodeError:
            return render_template('backups.html', title='Backups', error="Invalid JSON format in backup job file")





@app.route('/backups/helpers/path', methods=['GET'])
@login_required_route
def check_path_exists():
    # Get the 'file' and 'dir' parameters from the query string
    file_param = request.args.get('file')
    dir_param = request.args.get('dir')

    # Check if either 'file' or 'dir' parameter is provided
    if file_param:
        # Use os.path.isfile to check if the file exists
        path_exists = os.path.isfile(file_param)
        path_type = 'file'
    elif dir_param:
        # Use os.path.isdir to check if the directory exists
        path_exists = os.path.isdir(dir_param)
        path_type = 'dir'
    else:
        # If neither 'file' nor 'dir' parameter is provided, return an error response
        return jsonify({'error': 'Please provide either "file" or "dir" parameter in the query string'})

    # Return a JSON response indicating whether the path exists or not
    response_data = {'path': file_param or dir_param, 'type': path_type, 'exists': path_exists}
    return jsonify(response_data)


#https://server2.openpanel.co:2087/backups/restore/dates/nesto
@app.route('/backups/restore/dates/<username>', methods=['GET'])
@login_required_route
def get_backup_dates(username):
    try:
        # Run the command to get backup dates
        command = f"opencli backup-list {username} --json"
        result = subprocess.run(command, shell=True, check=True, capture_output=True, text=True)

        # Parse the JSON output
        backup_data = json.loads(result.stdout)

        return jsonify(backup_data)

    except subprocess.CalledProcessError as e:
        # Handle errors if the command fails
        return jsonify({"error": f"Failed to retrieve backup data for {username}. Error: {e.stderr}"}), 500

#https://server2.openpanel.co:2087/backups/restore/nesto/2/20240131164832
@app.route('/backups/restore/<username>/<job_id>/<date>', methods=['GET'])
@login_required_route
def get_backup_details(username, job_id, date):
    try:
        # Run the command to get backup details
        command = f"opencli backup-details {username} {job_id} {date} --json"
        result = subprocess.run(command, shell=True, check=True, capture_output=True, text=True)

        # Split the output into lines and parse each line as JSON
        lines = result.stdout.strip().split('\n')
        backup_details = [json.loads(line) for line in lines]

        return jsonify(backup_details)

    except subprocess.CalledProcessError as e:
        # Handle errors if the command fails
        return jsonify({"error": f"Failed to retrieve backup details for {username}/{job_id}/{date}. Error: {e.stderr}"}), 500


import configparser
@app.route('/backups/settings', methods=['GET', 'POST'])
@login_required_route
def backup_settings_from_config_ini_file():
    config_file_path = '/etc/openpanel/openadmin/backups/config.ini'
    
    if request.method == 'POST':
        # run opencli command for each setting to save it
        #
        # https://dev.openpanel.com/cli/backup.html#Update
        #
        # [GENERAL]    
        debug = 'debug' in request.form
        error_report = 'error_report' in request.form
        workplace_dir = request.form.get('workplace_dir')
        downloads_dir = request.form.get('downloads_dir')
        delete_orphan_backups = int(request.form.get('delete_orphan_backups'))
        days_to_keep_logs = int(request.form.get('days_to_keep_logs'))
        time_format = int(request.form.get('time_format'))

        # [PERFORMANCE]  
        avg_load_limit = int(request.form.get('avg_load_limit'))
        concurent_jobs = int(request.form.get('concurent_jobs'))
        backup_restore_ttl = int(request.form.get('backup_restore_ttl'))
        cpu_limit = int(request.form.get('cpu_limit'))
        io_read_limit = int(request.form.get('io_read_limit'))
        io_write_limit = int(request.form.get('io_write_limit'))

        # [NOTIFICATIONS]  
        enable_notification = 'enable_notification' in request.form
        email_notification = 'email_notification' in request.form
        send_emails_to = request.form.get('send_emails_to')
        notify_on_every_job = 'notify_on_every_job' in request.form
        notify_on_failed_backups = 'notify_on_failed_backups' in request.form
        notify_on_no_backups = 'notify_on_no_backups' in request.form
        notify_if_no_backups_after = int(request.form.get('notify_if_no_backups_after'))

        # existing data to compare with befor running opencli commands
        debug_current_value = config_data.get('GENERAL', {}).get('debug', '')
        error_report_current_value = config_data.get('GENERAL', {}).get('error_report', '')
        workplace_dir_current_value = config_data.get('GENERAL', {}).get('workplace_dir', '')
        downloads_dir_current_value = config_data.get('GENERAL', {}).get('downloads_dir', '')
        delete_orphan_backups_current_value = config_data.get('GENERAL', {}).get('delete_orphan_backups', '')
        days_to_keep_logs_current_value = config_data.get('GENERAL', {}).get('days_to_keep_logs', '')
        time_format_current_value = config_data.get('GENERAL', {}).get('time_format', '')
        avg_load_limit_current_value = config_data.get('PERFORMANCE', {}).get('avg_load_limit', '')
        concurent_jobs_current_value = config_data.get('PERFORMANCE', {}).get('concurent_jobs', '')
        backup_restore_ttl_current_value = config_data.get('PERFORMANCE', {}).get('backup_restore_ttl', '')
        cpu_limit_current_value = config_data.get('PERFORMANCE', {}).get('cpu_limit', '')
        io_read_limit_current_value = config_data.get('PERFORMANCE', {}).get('io_read_limit', '')
        io_write_limit_current_value = config_data.get('PERFORMANCE', {}).get('io_write_limit', '')
        enable_notification_current_value = config_data.get('NOTIFICATIONS', {}).get('enable_notification', '')
        email_notification_current_value = config_data.get('NOTIFICATIONS', {}).get('email_notification', '')
        send_emails_to_current_value = config_data.get('NOTIFICATIONS', {}).get('send_emails_to', '')
        notify_on_every_job_current_value = config_data.get('NOTIFICATIONS', {}).get('notify_on_every_job', '')
        notify_on_failed_backups_current_value = config_data.get('NOTIFICATIONS', {}).get('notify_on_failed_backups', '')
        notify_on_no_backups_current_value = config_data.get('NOTIFICATIONS', {}).get('notify_on_no_backups', '')
        notify_if_no_backups_after_current_value = config_data.get('NOTIFICATIONS', {}).get('notify_if_no_backups_after', '')



        success_messages = []
        error_messages = []
        command = f"opencli backup-config update '{setting}' '{value}'"
        success_message = f"Backup settings saved"
        error_message = f"Error: Backup settings could not be saved."
        try:
            result = subprocess.check_output(command, shell=True, text=True)
            if f"Updated {setting} to" in result:
                success_messages.append(success_message)
            else:
                error_messages.append(error_message)
        except subprocess.CalledProcessError as e:
            error_messages.append(f"Error executing command: '{command}': {e.output}")
    else:
        # Read the content of the configuration file
        try:
            config_parser = configparser.ConfigParser()
            config_parser.read(config_file_path)

            # Convert the configuration to a dictionary
            config_dict = {section: dict(config_parser.items(section)) for section in config_parser.sections()}

            # Return the JSON response
            return jsonify(config_dict)

        except FileNotFoundError:
            return "Configuration file not found", 404


@app.route('/backups/jobs', methods=['GET', 'POST', 'DELETE'])
@login_required_route
def show_all_jobs():    
    if request.method == 'POST':
        name = request.form.get('name')
        destination = request.form.get('destination')
        directory = request.form.get('directory')
        type_ = request.form.get('type')
        schedule = request.form.get('schedule')
        retention = request.form.get('retention')
        status = request.form.get('status')
        filters = request.form.get('filters')

        # Validate inputs
        errors = []
        if not name:
            errors.append('Name is required.')
        if not destination:
            errors.append('Destination is required.')
        if not directory:
            errors.append('Directory is required.')
        if type_ not in ["accounts", "configuration"]:
            errors.append('Invalid type. Must be "accounts" or "configuration".')
        if schedule not in ["daily", "weekly", "monthly", "yearly"]:
            errors.append('Invalid schedule. Must be daily, weekly, monthly, or yearly.')
        try:
            retention = int(retention)
            if retention < 0:
                errors.append('Retention must be a non-negative integer.')
        except ValueError:
            errors.append('Retention must be a non-negative integer.')
        if status not in ["on", "off"]:
            errors.append('Invalid status. Must be "on" or "off".')
        
        # If there are errors, return them
        if errors:
            return jsonify({'errors': errors}), 400

        # Construct the command
        command = f"opencli backup-job create '{name}' {destination} {directory} {type_} {schedule} {retention} {status} '{filters}'"
        
        # Execute the command and capture output
        try:
            output = subprocess.check_output(command, shell=True, stderr=subprocess.STDOUT, universal_newlines=True)
            return jsonify({'message': 'Backup job created successfully.', 'output': output}), 200
        except subprocess.CalledProcessError as e:
            error_message = f'Failed to create backup job. Command: {command}. Error: {e.output}'
            return jsonify({'error': error_message}), 500

    elif request.method == 'DELETE':
        id = request.form.get('id')
        command = f"opencli backup-job delete {id}"
        try:
            output = subprocess.check_output(command, shell=True, stderr=subprocess.STDOUT, universal_newlines=True)
            return jsonify({'message': 'Backup job deleted successfully.', 'output': output}), 200
        except subprocess.CalledProcessError as e:
            error_message = f'Failed to delete backup job. Command: {command}. Error: {e.output}'
            return jsonify({'error': error_message}), 500

    elif request.method == 'GET':
        try:
            backup_folder = '/etc/openpanel/openamdin/config/backups/jobs/'
            json_files = [f for f in os.listdir(backup_folder) if f.endswith('.json')]
            combined_data = []
            for json_file in json_files:
                file_path = os.path.join(backup_folder, json_file)
                with open(file_path, 'r') as file:
                    data = json.load(file)
                    data["id"] = os.path.splitext(json_file)[0]  # Strip ".json" extension
                    combined_data.append(data)

            return jsonify({"backup_jobs": combined_data})
        except FileNotFoundError:
            return jsonify({"error": "File not found"})
        except json.JSONDecodeError:
            return jsonify({"error": "Invalid JSON format in the file"})
    else:
        return jsonify({"error": "Unsupported request type."})



@app.route('/backups/jobs/run/<int:job_id>', methods=['POST'])
@login_required_route
def run_backupjob_on_demand(job_id):
    command = f"opencli backup-scheduler --run={job_id}"
    process = subprocess.Popen(command, shell=True, stdout=subprocess.PIPE, stderr=subprocess.PIPE)
    stdout, stderr = process.communicate()
    
    if process.returncode != 0:
        return jsonify({"message": f"Error: {stderr.decode('utf-8')}"}), 500
    
    backup_command = stdout.decode('utf-8').strip()
    backup_command += " --force-run"  # Append "--force-run" to the command in case job is disabled!
    
    backup_process = subprocess.Popen(backup_command, shell=True, stdout=subprocess.PIPE, stderr=subprocess.PIPE)
    return jsonify({"message": "Backup started successfully."}), 200


@app.route('/backups/jobs/<int:job_id>', methods=['GET'])
@login_required_route
def show_single_job(job_id):
    try:
        backup_folder = '/etc/openpanel/openadmin/config/backups/jobs/'

        # Form the file name based on the provided job_id
        json_file = f"{job_id}.json"
        file_path = os.path.join(backup_folder, json_file)

        # Check if the file exists
        if os.path.exists(file_path):
            with open(file_path, 'r') as file:
                data = json.load(file)
                data["id"] = os.path.splitext(json_file)[0]  # Strip ".json" extension
                return jsonify(data)
        else:
            return jsonify({"error": "File not found"})
    except json.JSONDecodeError:
        return jsonify({"error": "Invalid JSON format in the file"})





def get_backup_file_path(number=None):
    if number is not None:
        return f'/etc/openpanel/openadmin/config/backups/destinations/{number}.json'
    else:
        # List all JSON files in the directory
        directory = '/etc/openpanel/openadmin/config/backups/destinations/'
        json_files = [f for f in os.listdir(directory) if f.endswith('.json')]
        return [os.path.join(directory, file) for file in json_files]



@app.route('/backups/destination/<action>/<int:number>', methods=['POST'])
@login_required_route
def manage_backup_destination(action, number):
    if action == 'edit':
        return edit_backup_config(number)
    elif action == 'delete':
        return delete_backup_config(number)
    elif action == 'create':
        return create_backup_config()
    elif action == 'validate':
        return validate_backup_config(number)
    else:
        return jsonify({"result": "Invalid action", "status": "failure"}), 400

def edit_backup_config(number):
    # Get data from the request form
    id = request.form.get('id')
    hostname = request.form.get('hostname')
    port_str = request.form.get('port')
    port = int(port_str)
    user = request.form.get('user')
    path_to_ssh_key_file = request.form.get('path_to_ssh_key_file')
    storage_percentage = request.form.get('storage_percentage')

    # Construct the command
    command = [
        'opencli',
        'backup-destination',
        'edit',
        str(id),
        str(hostname),
        str(port),
        str(user),
        str(path_to_ssh_key_file),
        str(storage_percentage)
    ]

    try:
        # Run the command using subprocess
        result = subprocess.run(command, capture_output=True, text=True, check=True)
        output = result.stdout

        # Parse the output to check for success or failure
        if 'edited successfully' in output:
            # Success
            success_message = output.strip()
            return jsonify({"result": success_message, "status": "success"})
        else:
            # Failure
            failure_message = output.strip()
            return jsonify({"result": failure_message, "status": "failure"})

    except subprocess.CalledProcessError as e:
        # Handle errors
        error_message = e.stdout if e.stdout else str(e)
        return jsonify({"result": error_message, "status": "error"})

def delete_backup_config(number):
    command = [
        'opencli',
        'backup-destination',
        'delete',
        str(id)
    ]

    try:
        # Run the command using subprocess
        result = subprocess.run(command, capture_output=True, text=True, check=True)
        output = result.stdout

        # Check for success or failure
        if 'Deleted destination ID' in output:
            # Specific success condition
            success_message = output.strip()
            return jsonify({"result": success_message, "status": "success"})
        else:
            # Failure
            failure_message = output.strip()
            return jsonify({"result": failure_message, "status": "failure"})

    except subprocess.CalledProcessError as e:
        # Handle errors
        error_message = e.stdout if e.stdout else str(e)
        return jsonify({"result": error_message, "status": "error"})

def create_backup_config():
    hostname = request.form.get('hostname')
    port_str = request.form.get('port')
    port = int(port_str)
    user = request.form.get('user')
    path_to_ssh_key_file = request.form.get('path_to_ssh_key_file')
    storage_percentage = request.form.get('storage_percentage')

    # Construct the command
    command = [
        'opencli',
        'backup-destination',
        'create',
        str(hostname),
        str(port),
        str(user),
        str(path_to_ssh_key_file),
        str(storage_percentage)
    ]

    try:
        # Run the command using subprocess
        result = subprocess.run(command, capture_output=True, text=True, check=True)
        output = result.stdout

        # Parse the output to check for success or failure
        if 'Successfully created' in output:
            # Success
            success_message = output.strip()
            return jsonify({"result": success_message, "status": "success"})
        else:
            # Failure
            failure_message = output.strip()
            return jsonify({"result": failure_message, "status": "failure"})

    except subprocess.CalledProcessError as e:
        # Handle errors
        error_message = e.stdout if e.stdout else str(e)
        return jsonify({"result": error_message, "status": "error"})

def validate_backup_config(id):
    command = [
        'opencli',
        'backup-destination',
        'validate',
        str(id)
    ]

    try:
        # Run the command using subprocess
        result = subprocess.run(command, capture_output=True, text=True, check=True)
        output = result.stdout

        # Check for success or failure
        if 'SSH connection successful' in output:
            # Specific success condition
            success_message = output.strip()
            return jsonify({"result": success_message, "status": "success"})
        elif 'Validated' in output:
            # Another specific success condition (if needed)
            success_message = output.strip()
            return jsonify({"result": success_message, "status": "success"})
        else:
            # Failure
            failure_message = output.strip()
            return jsonify({"result": failure_message, "status": "failure"})

    except subprocess.CalledProcessError as e:
        # Handle errors
        error_message = e.stdout if e.stdout else str(e)
        return jsonify({"result": error_message, "status": "error"})



@app.route('/backups/destination', defaults={'number': None}, methods=['GET'])
@app.route('/backups/destination/<int:number>', methods=['GET'])
@login_required_route
def get_backup_config(number):
    file_paths = get_backup_file_path(number)

    if isinstance(file_paths, list):
        # If a list of file paths is returned, merge content of all files
        configurations = []
        for file_path in file_paths:
            try:
                with open(file_path, 'r') as file:
                    file_content = file.read()
                    if file_content:
                        config_id = get_config_id(file_path)
                        configurations.append({"id": config_id, "configuration": json.loads(file_content)})
                    else:
                        configurations.append({"error": f"File {file_path} is empty"})
            except FileNotFoundError:
                configurations.append({"error": f"File {file_path} not found"})
            except json.JSONDecodeError:
                configurations.append({"error": f"Invalid JSON in file {file_path}"})
    else:
        # If a single file path is returned, read and return content of that file
        try:
            with open(file_paths, 'r') as file:
                file_content = file.read()
                if file_content:
                    config_id = get_config_id(file_paths)
                    configurations = {"id": config_id, "configuration": json.loads(file_content)}
                else:
                    return jsonify({"error": f"File for number {number} is empty"})
        except FileNotFoundError:
            return jsonify({"error": f"File for number {number} not found"}), 404
        except json.JSONDecodeError:
            return jsonify({"error": f"Invalid JSON in file for number {number}"}), 400

    return jsonify(configurations)

def get_config_id(file_path):
    # Extract the file name without the ".json" extension as the ID
    return os.path.splitext(os.path.basename(file_path))[0]





@app.route('/backups/logs', defaults={'number': None}, methods=['GET'])
@login_required_route
def get_all_backup_logs(number):
    base_path = '/var/log/openpanel/admin/backups/'

    # Check if the directory exists, create it if it doesn't
    if not os.path.exists(base_path):
        try:
            os.makedirs(base_path)
        except OSError as e:
            return jsonify({'error': f'Failed to create directory: {e}'}), 500

    folders = os.listdir(base_path)
    
    data = []  # Updated data structure to store information about each directory

    for folder in folders:
        folder_path = os.path.join(base_path, folder)
        if os.path.isdir(folder_path):
            # Get a list of .log files in the current directory
            log_files = [f for f in os.listdir(folder_path) if f.endswith('.log')]

            # Iterate through each .log file and read the first 10 lines
            log_data = []
            for log_file in log_files:
                log_file_path = os.path.join(folder_path, log_file)
                with open(log_file_path, 'r') as file:
                    # Read the first 10 lines
                    first_10_lines = [file.readline().strip() for _ in range(10)]

                    # Extract specific data from the first 10 lines
                    log_info = {}
                    for line in first_10_lines:
                        parts = line.split('=')
                        if len(parts) == 2:
                            key, value = parts
                            log_info[key.strip()] = value.strip()

                    log_data.append({'file': log_file, 'log_info': log_info})

            # Add information about the directory, .log files, and specific data from the first 10 lines to the data list
            directory_info = {'job_id': folder, 'log_files': log_data}
            data.append(directory_info)

    return jsonify(data)


@app.route('/backups/logs/<int:number>', methods=['GET'])
@login_required_route
def get_backup_logs_job(number):
    base_path = f'/var/log/openpanel/admin/backups/{number}/'
    files = [file.split('.')[0] for file in os.listdir(base_path) if file.endswith('.log')]
    data = {'files': files}
    return jsonify(data)

@app.route('/backups/logs/<int:number>/<int:file_number>.log', methods=['GET'])
@login_required_route
def get_backup_logs_for_job(number, file_number):
    file_path = f'/var/log/openpanel/admin/backups/{number}/{file_number}.log'
    
    try:
        with open(file_path, 'r') as file:
            file_content = file.read()
        return Response(file_content, content_type='text/plain'), 200
    except FileNotFoundError:
        return "File not found", 404
    except Exception as e:
        return str(e), 500

@app.route('/backups/logs/download/<int:number>/<int:file_number>.log', methods=['GET', 'DELETE'])
@login_required_route
def handle_backup_log_action(number, file_number):
    file_path = f'/var/log/openpanel/admin/backups/{number}/{file_number}.log'
    
    if request.method == 'GET':
        # Download action
        try:
            return send_file(file_path, as_attachment=True), 200
        except FileNotFoundError:
            return jsonify({"message": "Backup Log File not found"}), 404
        except Exception as e:
            return jsonify({"message": str(e)}), 500
    elif request.method == 'DELETE':
        # Delete action
        try:
            os.remove(file_path)
            return jsonify({"message": "Backup Log file deleted successfully"}), 200
        except FileNotFoundError:
            return jsonify({"message": "Backup Log file not found"}), 404
        except Exception as e:
            return jsonify({"message": str(e)}), 500
    else:
        return jsonify({"message": "Invalid method"}), 405


