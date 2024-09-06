################################################################################
# Author: Stefan Pejcic
# Created: 27.08.2024
# Last Modified: 27.08.2024
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
import json
import docker
from flask import Flask, Response, abort, render_template, request, send_file, g, jsonify, session, url_for, flash, redirect, get_flashed_messages
import subprocess
import datetime
import psutil

# import our modules
from app import app, login_required_route, connect_to_database
from modules.helpers import get_all_emails


# on every route we need to first check if mailserver is running!
def check_mailserver_status():
    try:
        # Run the 'docker ps' command to check if the container is running
        result = subprocess.run(
            ['docker', 'ps', '--filter', 'name=openadmin_mailserver', '--filter', 'status=running', '--format', '{{.Names}}'],
            stdout=subprocess.PIPE,
            stderr=subprocess.PIPE,
            text=True
        )
        # Check if the container name appears in the output
        if 'openadmin_mailserver' in result.stdout:
            return 'running'
        else:
            return 'stopped'
    except Exception as e:
        logging.error(f"Failed to check mailserver status: {e}")
        return 'unknown'


# view emails
@app.route('/emails/accounts', methods=['GET', 'POST'])
@login_required_route
def emails():
    current_route = request.path
    try:
        mailserver_status = check_mailserver_status()
        emails_and_quotas = get_all_emails()
        output_param = request.args.get('output') # json
        if output_param == 'json':
            return jsonify({'emails': emails_and_quotas})
        else:
            return render_template('emails/accounts.html', title='Email Accounts', mailserver_status=mailserver_status, emails=emails_and_quotas, app=app, current_route=current_route)

    except Exception as e:
        print(f"An error occurred: {e}")
        mailserver_status = 'unknown'
        emails_and_quotas = []
        output_param = request.args.get('output')
        if output_param == 'json':
            return jsonify({'emails': emails_and_quotas})
        else:
            return render_template('emails/accounts.html', title='Email Accounts', mailserver_status=mailserver_status, emails=emails_and_quotas, app=app, current_route=current_route)
 

# view queue
@app.route('/emails/queue', methods=['GET', 'POST'])
@login_required_route
def emails_queue():
    current_route = request.path
    try:
        mailserver_status = check_mailserver_status()
        
        queue = None #get_email_queue()

        output_param = request.args.get('output') # json
        if output_param == 'json':
            return jsonify({'queue': queue})
        else:
            return render_template('emails/queue.html', title='Email Queue', mailserver_status=mailserver_status, queue=queue, app=app, current_route=current_route)

    except Exception as e:
        print(f"An error occurred: {e}")
        queue = []
        mailserver_status = 'unknown'
        output_param = request.args.get('output')
        if output_param == 'json':
            return jsonify({'queue': queue})
        else:
            return render_template('emails/queue.html', title='Email Queue', mailserver_status=mailserver_status, queue=queue, app=app, current_route=current_route)
 


# view track delivery
@app.route('/emails/reports', methods=['GET', 'POST'])
@login_required_route
def emails_reports():
    current_route = request.path
    try:
        mailserver_status = check_mailserver_status()
        
        reports = None #get_email_reports()

        output_param = request.args.get('output') # json
        if output_param == 'json':
            return jsonify({'reports': reports})
        else:
            return render_template('emails/reports.html', title='Email Reports', mailserver_status=mailserver_status, queue=queue, app=app, current_route=current_route)

    except Exception as e:
        print(f"An error occurred: {e}")
        reports = []
        mailserver_status = 'unknown'
        output_param = request.args.get('output')
        if output_param == 'json':
            return jsonify({'reports': reports})
        else:
            return render_template('emails/reports.html', title='Email Reports', mailserver_status=mailserver_status, queue=queue, app=app, current_route=current_route)
 









# configuration
@app.route('/emails/settings', methods=['GET', 'POST'])
@login_required_route
def emails_settings():
    current_route = request.path
    try:
        mailserver_status = check_mailserver_status()
        return render_template('emails/settings.html', title='Email Settings', mailserver_status=mailserver_status, current_route=current_route)

    except Exception as e:
        print(f"An error occurred: {e}")
        mailserver_status = 'unknown'
        return render_template('emails/settings.html', title='Email Settings', mailserver_status=mailserver_status, current_route=current_route)
 




# auto-login to webmail
@app.route('/emails/webmail', methods=['GET', 'POST'])
@app.route('/emails/webmail/', methods=['GET', 'POST'])
@login_required_route
def emails_webmail():
    
    admin_token = request.args.get('admin_token') or request.form.get('admin_token')
    username_to_login = request.args.get('email') or request.form.get('email')

    # login to email
    if username_to_login:
        token_path = f'/etc/openpanel/openpanel/core/users/{username_to_login}/logintoken.txt'

        if os.path.exists(token_path):
            with open(token_path, 'r') as file:
                login_token = file.read().strip()

        if admin_token == login_token:
            try:
                # TODO AUTH HERE
                # 1. get master
                # 2. login with master in new tab
                # 3. rm master * from username
                return jsonify({'not': 'yet_ready'})

            except Exception as e:
                error_message = _('Error occurred while trying to auto-login to webmail.')
        else:
            error_message = _('Token is invalid.')


    # list webmail settings
    else:
        webmail_services = {
            'roundcube': 'roundcube',
            'sogo': 'sogo',
            'snappymail': 'snappymail'
        }
        
        try:
            # Run the `docker ps` command to get a list of running containers
            result = subprocess.run(['docker', 'ps', '--format', '{{.Image}}'], capture_output=True, text=True)
            running_images = result.stdout.splitlines()
            
            running_service = 'none' # default
            for service_key, service_name in webmail_services.items():
                if any(service_key in image for image in running_images):
                    running_service = service_name
                    break
            
        except subprocess.CalledProcessError as e:
            running_service = f'error running docker ps: {e}'

        output_param = request.args.get('output') # json
        if output_param == 'json':
            return jsonify({'webmail': running_service})
        else:
            return render_template('emails/webmail.html', title='Webmail Settings', queue=queue, app=app, current_route=current_route)




# Define the base directory for mailserver configurations
nginx_base_dir = '/usr/local/mail/openmail/'


@app.route('/services/mailserver/conf', methods=['GET', 'POST'])
@login_required_route
def dms_conf():
    file_paths_allowed = [
        nginx_base_dir + 'mailserver.env',
        nginx_base_dir + 'compose.yml'
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



