################################################################################
# *************************************************************************
# *                                                                       *
# * OpenAdmin                                                             *
# * Copyright (c) OpenPanel. All Rights Reserved.                         *
# * Version: 1.3.2                                                        *
# * Build Date: 2025-05-27 19:36:31                                       *
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
# Last Modified: 22.04.2025
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
from flask import Flask, Response, abort, render_template, request, send_file, g, jsonify, session, url_for, flash, redirect, make_response, get_flashed_messages
import subprocess
import datetime
from operator import itemgetter
from subprocess import check_output
from datetime import datetime
import shlex
import psutil
from app import app, is_license_valid, admin_required_route, load_openpanel_config, connect_to_database
import docker

from modules.helpers import get_all_users


@app.route('/security/waf/view-rules', methods=['GET', 'POST'])
@admin_required_route
def admin_edit_waf_rules():
    current_route = request.path

    rules_dir = '/etc/openpanel/caddy/coreruleset/rules/'

    if request.method == 'GET':
        filename = request.args.get('edit')
        
        if filename:
            if os.path.commonprefix([os.path.realpath(filename), rules_dir]) == rules_dir:

                if filename.endswith('.conf') or filename.endswith('.conf.disabled'):
                    file_path = os.path.join(rules_dir, filename)
                    if os.path.exists(file_path) and os.path.isfile(file_path):
                        # Read and display the file content as plain text
                        with open(file_path, 'r') as file:
                            file_content = file.read()
                        return f'<pre>{file_content}</pre>'  # Return content wrapped in <pre> for plain text display
                    else:
                        flash("Files updated successfully!", "error")
                        return redirect(url_for('admin_waf_rules'))
                else:
                    flash("Hacker? Invalid file extension. Only .conf and .conf.disabled files are allowed.", "error")
                    return redirect(url_for('admin_waf_rules'))
            else:
                flash("Hacker? Invalid file path", "error")
                return redirect(url_for('admin_waf_rules'))
        else:
            flash("Hacker? No filename provided in the query parameters.", "error")
            return redirect(url_for('admin_waf_rules'))
    
    return 'Invalid request method', 405



@app.route('/security/waf/rules', methods=['GET', 'POST'])
@admin_required_route
def admin_waf_rules():
    current_route = request.path

    # Path to the directory containing the rule files
    rules_dir = '/etc/openpanel/caddy/coreruleset/rules/'

    rule_files = [
        f for f in os.listdir(rules_dir) if f.endswith('.conf') or f.endswith('.conf.disabled')
    ]

    # If it's a POST request, handle the file renaming (on/off toggle)
    if request.method == 'POST':
        # Get the name of the rule to toggle and the action (on/off)
        rule_name = request.form.get('rule_name')  # Name of the rule to toggle
        action = request.form.get('action')  # 'on' or 'off'

        # Find the corresponding file
        rule_file = next(
            (f for f in rule_files if f[:-len('.conf')] == rule_name or f[:-len('.conf.disabled')] == rule_name), None
        )

        if rule_file:
            rule_file_path = os.path.join(rules_dir, rule_file)

            if action == 'off' and rule_file.endswith('.conf'):
                new_file_path = rule_file_path + '.disabled'
                os.rename(rule_file_path, new_file_path)
                flash("Rules set disabled. Restart Caddy to apply changes.", "success")
            elif action == 'on' and rule_file.endswith('.conf.disabled'):
                new_file_path = rule_file_path.rstrip('.disabled') # Remove .disabled
                os.rename(rule_file_path, new_file_path)
                flash("Rules set enabled. Restart Caddy to apply changes.", "success")
            else:
                flash("Hacker! Invalid action.", "error")
        else:
            flash("rule_file is missing from the POST request.", "error")

        return redirect(url_for('admin_waf_rules'))

    # GET
    rules_details = []
    for file in rule_files:
        file_path = os.path.join(rules_dir, file)
        
        # Extract the name: part of the file name (before .conf or .conf.disabled)
        name = file[:-len('.conf')] if file.endswith('.conf') else file[:-len('.conf.disabled')]

        # Determine the status based on file extension
        status = 'off' if file.endswith('.conf.disabled') else 'on'

        # Count the number of non-empty lines (rules)
        with open(file_path, 'r') as f:
            num_rules = sum(1 for line in f if line.strip())  # Count non-empty lines
        
        # Add the file details to the list
        rules_details.append({
            'name': name,
            'path': file_path,
            'num_rules': num_rules,
            'status': status  # Add the status to the details
        })

    # If the output is requested as JSON, return the rules details in JSON format
    if request.args.get('output') == 'json':
        return jsonify(rules_details)

    # Otherwise, render the template with the rule details
    return render_template(
        'security/coraza_rules.html',
        title='CorazaWAF Rules',
        current_route=current_route,
        rules_details=rules_details
    )


   
@app.route('/security/waf', methods=['GET', 'POST'])
@admin_required_route
def admin_waf_status():
    current_route = request.path
    status = 'off'
    rules_dir = '/etc/openpanel/caddy/coreruleset/rules/'

    # Initialize counts for .conf and .conf.disabled files
    conf_count = 0
    conf_disabled_count = 0
    total_count = 0

    # Check if the directory exists
    if os.path.isdir(rules_dir):
        try:
            # Loop through the files in the directory
            for filename in os.listdir(rules_dir):
                if filename.endswith('.conf'):
                    conf_count += 1
                    total_count += 1
                elif filename.endswith('.conf.disabled'):
                    conf_disabled_count += 1
                    total_count += 1
        except Exception as e:
            print(f"Error reading files in {rules_dir}: {e}")
    
    # Read the .env file to determine the status of the WAF
    try:
        with open('/root/.env', 'r') as env_file:
            for line in env_file:
                line = line.strip()
                if line.startswith('CADDY_IMAGE='):
                    value = line.split('=', 1)[1].strip().strip('"')
                    if value == 'openpanel/caddy-coraza':
                        status = 'on'
                    break
    except Exception as e:
        print(f"Error reading .env file: {e}")

    # If the request is for JSON output, return status and file counts
    if request.args.get('output') == 'json':
        return jsonify({
            'status': status,
            'total_sets': total_count,
            'active_sets': conf_count,
            'inactive_sets': conf_disabled_count
        })

    # Otherwise, render the template with the status and file counts
    return render_template(
        'security/coraza_waf.html',
        title='CorazaWAF Status',
        current_route=current_route,
        status=status,
        active_sets=conf_count,
        inactive_sets=conf_disabled_count,
        total_sets=total_count
    )
