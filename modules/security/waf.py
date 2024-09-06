################################################################################
# Author: Stefan Pejcic
# Created: 11.07.2023
# Last Modified: 09.03.2024
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
from flask import Flask, Response, abort, render_template, request, send_file, g, jsonify, session, url_for, flash, redirect, make_response, get_flashed_messages
import subprocess
import datetime
from operator import itemgetter
from subprocess import check_output
from datetime import datetime
import shlex
import psutil
from app import app, is_license_valid, login_required_route, load_openpanel_config, connect_to_database
import docker

from modules.helpers import get_all_users, get_user_and_plan_count, get_plan_by_id, get_all_plans, get_userdata_by_username, get_hosting_plan_name_by_id, get_user_websites, is_username_unique, gravatar_url


# /security/modsecurity/waf_rules or /security/modsecurity/waf_rules?id=all
@app.route('/security/modsecurity/waf_rules')
@login_required_route
def waf_rules():
    # enable conf
    rule_file_to_enable = request.args.get('enable')

    if rule_file_to_enable:       
        try:
            output = subprocess.check_output(['opencli', 'nginx-modsec', '--enable', rule_file_to_enable, '--json'])
            file_data = json.loads(output.decode())
            return file_data
        except subprocess.CalledProcessError:
            return "Error enabling rule", 500
        except json.JSONDecodeError:
            return "Error decoding response", 500

    # disable conf
    rule_file_to_disable = request.args.get('disable')

    if rule_file_to_disable:
        try:
            output = subprocess.check_output(['opencli', 'nginx-modsec', '--disable', rule_file_to_disable, '--json'])
            file_data = json.loads(output.decode())
            return file_data
        except subprocess.CalledProcessError:
            return "Error disabling rule", 500
        except json.JSONDecodeError:
            return "Error decoding response", 500

    






    # return conf files
    rule_file = request.args.get('file')

    if rule_file:
        rule_file = shlex.quote(rule_file)

        try:
            output = subprocess.check_output(['opencli', 'nginx-modsec', '--json'])
            file_data = json.loads(output.decode())
            file_list = [item["file"] for item in file_data]
        except subprocess.CalledProcessError:
            return "Error retrieving file list", 500
        except json.JSONDecodeError:
            return "Error decoding file list", 500

        if rule_file in file_list:
            # Proceed only if the requested file exists and is a regular file
            if os.path.exists(rule_file) and os.path.isfile(rule_file):
                with open(rule_file, 'r') as f:
                    file_content = f.read()
                return file_content
            else:
                return "File not found", 404
        else:
            return "Access Denied: File is not a ModSecurity configuration file!", 403

    # return all rule ids
    rules_individual = request.args.get('id')

    if rules_individual:
        rules_individual = shlex.quote(rules_individual)

    log_command = "opencli nginx-modsec"

    if rules_individual:
        log_command += f" --rules"
    log_command += f" --json"

    log_process = subprocess.Popen(log_command, stdout=subprocess.PIPE, stderr=subprocess.PIPE, shell=True)
    log_stdout, log_stderr = log_process.communicate()

    if log_stderr:
        return jsonify({"error": f"Error fetching rules: {log_stderr.decode()}"}), 500

    return log_stdout, 200, {'Content-Type': 'application/json'}

@app.route('/security/modsecurity/waf_logs')
@login_required_route
def waf_logs():
    # Get 'page', 'per_page', and 'search' query parameters
    page = request.args.get('page', default=1, type=int)
    per_page = request.args.get('per_page', default=20, type=int)
    search_word = request.args.get('search')

    # Sanitize search word to prevent command injection
    if search_word:
        search_word = shlex.quote(search_word)

    count_command = "opencli nginx-modsec --logs"
    if search_word:
        count_command += f" {search_word} | wc -l"
    else:
        count_command += f" | wc -l"

    count_process = subprocess.Popen(count_command, stdout=subprocess.PIPE, stderr=subprocess.PIPE, shell=True)
    count_stdout, count_stderr = count_process.communicate()

    if count_stderr:
        return jsonify({"error": f"Error fetching total log count: {count_stderr.decode()}"}), 500

    total_lines = int(count_stdout.strip())
    total_pages = (total_lines + per_page - 1) // per_page

    start_line = (page - 1) * per_page + 1
    end_line = min(page * per_page, total_lines)

    if total_pages < 1:
        return jsonify({"error": "No results"}), 400

    if page > total_pages:
        return jsonify({"error": "Requested page is out of range"}), 400

    log_command = f"opencli nginx-modsec --logs"
    if search_word:
        log_command += f" {search_word}"
    log_command += f" | sed -n '{start_line},{end_line}p'"

    log_process = subprocess.Popen(log_command, stdout=subprocess.PIPE, stderr=subprocess.PIPE, shell=True)
    log_stdout, log_stderr = log_process.communicate()

    if log_stderr:
        return jsonify({"error": f"Error fetching logs: {log_stderr.decode()}"}), 500

    logs_output = log_stdout.decode()

    return jsonify({
        "logs": logs_output,
        "pagination": {
            "current_page": page,
            "per_page": per_page,
            "total_pages": total_pages,
            "total_entries": total_lines
        }
    })



@app.route('/security/modsecurity', methods=['GET', 'POST'])
@login_required_route
def admin_modsecurity_settings():
    current_route = request.path
    log_file_path = '/usr/local/admin/modsec_install_log'
    process = None

    try:
        # Attempt to get all users
        users = get_all_users()

        if request.method == 'POST':
            action = request.form.get('action')
            if action == "install":
                command_to_install_modsec = "opencli nginx-install_modsec"
                try:
                    with open(log_file_path, 'w') as log_file:
                        process = subprocess.Popen(command_to_install_modsec, shell=True, stdout=log_file, stderr=subprocess.STDOUT)
                except Exception as e:
                    return f"Installation failed to start. Check {log_file_path} for details."
                return redirect('/security/modsecurity')
            elif action == "update_rules":
                command_to_update_owasp_rules = "opencli nginx-modsec --update"
                try:
                    subprocess.run(command_to_update_owasp_rules, shell=True, stderr=subprocess.STDOUT)
                except Exception as e:
                    return f"Updating ModSecurity rules failed."
                return redirect('/security/modsecurity')
            elif action == "activate_for_user":
                username = request.form.get('username')
                command_to_enable_modsec_for_user = f"opencli domains-enable_modsec '{username}'"
                try:
                    subprocess.run(command_to_enable_modsec_for_user, shell=True, stderr=subprocess.STDOUT)
                except Exception as e:
                    return f"Enabling ModSecurity for user {username} failed."
                return redirect('/security/modsecurity')
        else:
            modsec_enabled = os.path.exists('/etc/nginx/modsec/main.conf')
            is_modsec_installed = "YES" if modsec_enabled else "NO"

            if os.path.exists(log_file_path):
                if process is not None and process.poll() is not None and process.returncode == 0:
                    try:
                        os.rename(log_file_path, '/usr/local/admin/modsec_install_log.complete')
                    except Exception as e:
                        return f"ModSecurity installation completed, but the file {log_file_path} could not be deleted."
                return render_template('security/modsecurity_settings.html', title='ModSecurity Settings', app=app, is_modsec_installed=is_modsec_installed, modsec_install_log=log_file_path, users=users)

        return render_template('security/modsecurity_settings.html', title='ModSecurity Settings', app=app, is_modsec_installed=is_modsec_installed, users=users, current_route=current_route)

    except Exception as e:
        # Handle the exception, for example, log the error
        print(f"An error occurred: {e}")

        # Set default values
        users = []

        return render_template('security/modsecurity_settings.html', title='ModSecurity Settings', app=app, current_route=current_route, is_modsec_installed="NO", users=users)

