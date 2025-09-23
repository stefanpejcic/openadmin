################################################################################
# *************************************************************************
# *                                                                       *
# * OpenAdmin                                                             *
# * Copyright (c) OpenPanel. All Rights Reserved.                         *
# * Version: 1.6.0                                                        *
# * Build Date: 2025-09-23 11:22:23                                       *
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




# import python modules
import os
import json
from flask import Flask, Response, abort, render_template, request, send_file, g, jsonify, session, url_for, flash, redirect, get_flashed_messages
import subprocess
import datetime
import psutil
from flask_login import current_user

# import our modules
from app import app, cache, is_license_valid, login_required_route, connect_to_database
from modules.helpers import get_all_plans_and_user_count, get_plan_by_id, query_context_by_username, check_if_owner_for_user

def fetch_feature_sets():
    try:
        print(f"PLANS - Fetching feature sets..")
        feature_sets = [f[:-4] for f in os.listdir('/etc/openpanel/openpanel/features/') if f.endswith('.txt')]
    except FileNotFoundError:
        print(f"PLANS - No feature sets found!")
        feature_sets = []
    return feature_sets

# create plan
@app.route('/plans/new', methods=['GET', 'POST'])
@login_required_route
def create_plan():

    form_data = {}  # Store form data

    # POST
    if request.method == 'POST':
        form_data = {
            'name': request.form.get('name'),
            'description': request.form.get('description'),
            'email_limit': request.form.get('email_limit'),
            'ftp_limit': request.form.get('ftp_limit'),
            'domains_limit': request.form.get('domains_limit'),
            'websites_limit': request.form.get('websites_limit'),
            'disk_limit': request.form.get('disk_limit'),
            'inodes_limit': request.form.get('inodes_limit'),
            'db_limit': request.form.get('db_limit'),
            'cpu': request.form.get('cpu'),
            'ram': request.form.get('ram'),
            'bandwidth': request.form.get('bandwidth'),
            'feature_set': request.form.get('feature_set'),
        }

        command = f"opencli plan-create name='{form_data['name']}' description='{form_data['description']}' emails={form_data['email_limit']} ftp={form_data['ftp_limit']} domains={form_data['domains_limit']} websites={form_data['websites_limit']} disk={form_data['disk_limit']} inodes={form_data['inodes_limit']} databases={form_data['db_limit']} cpu={form_data['cpu']} ram={form_data['ram']} bandwidth={form_data['bandwidth']} feature_set='{form_data['feature_set']}'"
        print(f"PLANS - Executing: {command}")

        if getattr(current_user, 'role', 'reseller') == 'reseller':
            command += f" reseller={current_user.username}" 

        try:
            result = subprocess.run(command, shell=True, text=True, capture_output=True, check=True)
            output_message = result.stdout.strip() or "Plan created successfully."
            print(f"PLANS - Command returned: {output_message}")

            flash(output_message, 'success')
            return redirect(url_for('plans'))
        except subprocess.CalledProcessError as e:
            error_message = e.stderr.strip() if e.stderr else e.output.strip()
            print(f"PLANS - Command returned error: {error_message}")
            flash(error_message, 'error')
            return redirect(url_for('plans'))
            
    # GET
    current_route = request.path

    new_plan_template_path = '/etc/openpanel/openadmin/config/new_plan_template'
    new_plan_template_content = {}
    if os.path.exists(new_plan_template_path):
        print(f"PLANS - Prefilling form with data from: {new_plan_template_path}")
        with open(new_plan_template_path, 'r') as file:
            new_plan_template_content = json.load(file)

    feature_sets = fetch_feature_sets()

    return render_template('new_plan.html', title='Create New Package', app=app, current_route=request.path, form_data=form_data,plan_template_data=new_plan_template_content, feature_sets=feature_sets)


@app.route('/plan/apply/<filename>')
@login_required_route
def serve_log_file(filename):
    log_file_path = f'/tmp/{filename}'
    print(f"PLANS - Opening file: {log_file_path}")
    return send_file(log_file_path, mimetype='text/plain')



# delete plan
@app.route('/plan/delete/<plan_name>', methods=['POST'])
@login_required_route
def delete_plan(plan_name):
    command = f"opencli plan-delete '{plan_name}' --json"
    print(f"PLANS - Executing: {command}")

    try:
        output = subprocess.check_output(command, shell=True, text=True)
        message = 'Plan deleted successfully.'
        flash(message, 'success')

    except subprocess.CalledProcessError as e:
        message = f"Error executing command: {command} <br>{e.output}"
        print(f"PLANS - {message}")
        flash(message, 'error')
    return redirect(url_for('plans'))


# single plan
@app.route('/plans/<plan_id>', methods=['GET', 'POST'])
@login_required_route
def edit_plan(plan_id):
    current_route = request.path
    
    # POST
    if request.method == 'POST':
        name = request.form.get('name')
        description = request.form.get('description')
        print(f"PLANS - Editing plan name: {name}")
        
        # 0 is default for unlimited
        email_limit = request.form.get('edit_email_limit') or "0"
        ftp_limit = request.form.get('edit_ftp_limit') or "0"
        domains_limit = request.form.get('domains_limit') or "0"
        websites_limit = request.form.get('websites_limit') or "0"
        disk_limit = request.form.get('edit_disk_limit') or "0"
        inodes_limit = request.form.get('edit_storage_file_inodes') or "0"
        db_limit = request.form.get('edit_db_limit') or "0"

        # Use "1" for these, so low-resource servers dont error
        cpu = request.form.get('edit_cpu') or "1"
        ram = request.form.get('edit_ram') or "1"

        # legacy
        disk_limit = disk_limit if disk_limit != "0" and not disk_limit.endswith(" GB") else disk_limit
        ram = request.form.get('edit_ram') or "1"

        bandwidth = request.form.get('edit_bandwidth') or "100"
        feature_set = request.form.get('edit_feature_set') or "default"

        command = [
            'opencli', 'plan-edit',
            f'id={plan_id}',
            f'name={name}',
            f'description={description}',
            f'emails={email_limit}',
            f'ftp={ftp_limit}',
            f'domains={domains_limit}',
            f'websites={websites_limit}',
            f'disk={disk_limit}',
            f'inodes={inodes_limit}',
            f'databases={db_limit}',
            f'cpu={cpu}',
            f'ram={ram}',
            f'bandwidth={bandwidth}',
            f'feature_set={feature_set}'
        ]

        print(f"PLANS - Executing: {command}")
        try:
            message = subprocess.check_output(command, text=True)
            flash(message, 'success')

        except subprocess.CalledProcessError as e:
            message = f"Error executing command: {command} <br>{e.output}"
            print(f"PLANS - {message}")
            flash(e.output, 'error')

    # GET
    plan = []
    
    try:
        plan = get_plan_by_id(plan_id)
    except Exception as e:
        print(f"PLANS - An error occurred fetching plan ID:{plan_id} information from the database: {e}")        

    feature_sets = fetch_feature_sets()

    output_param = request.args.get('output')
    if output_param == 'json':
        return jsonify({'plan': plan})
    else:
        return render_template('edit_plan.html', title=f'Edit plan ID {plan_id}', plan=plan, app=app, current_route=current_route, feature_sets=feature_sets)
 





# view plans
@app.route('/plans', methods=['GET', 'POST'])
@login_required_route
def plans():
    current_route = request.path

    plans = []
    mysql_is_down = False
    
    plans = get_all_plans_and_user_count()
    if plans == -1:
        plans = []
        mysql_is_down = True
        
    output_param = request.args.get('output')
    if output_param == 'json':
        return jsonify({'plans': plans})
    else:
        return render_template('plans.html', title='Plans', plans=plans, app=app, current_route=current_route, mysql_is_down=mysql_is_down)
 


# get server ipv4

import ipaddress

@app.route('/system/ips/<username>', methods=['GET'])
@login_required_route
@cache.memoize(timeout=3600)
def get_ip_addresses(username):

    if not check_if_owner_for_user(username):
        print(f"PLANS - Aborting, user does not own account: {username} (data is cached for 3600s)")        
        abort(403)

    context = query_context_by_username(username)

    try:
        # Check docker context details
        inspect_cmd = ['docker', 'context', 'inspect', context, '--format', '{{json .}}']
        print(f"PLANS - Executing: {inspect_cmd}")
        inspect_result = subprocess.run(inspect_cmd, check=True, stdout=subprocess.PIPE, text=True)
        context_info = json.loads(inspect_result.stdout)

        # Determine if context is local or remote
        is_local = context_info.get("Endpoints", {}).get("docker", {}).get("Host", "").startswith("unix://")

        if is_local:
            print(f"PLANS - Local context detected: using server assigned primary IP.")
            hostname_cmd = ['hostname', '-I']
        else:
            print(f"PLANS - Remote context detected: using IP from ssh command.")
            host = context_info["Endpoints"]["docker"]["Host"]
            ssh_target = host.replace("ssh://", "").split('/')[0]
            hostname_cmd = ['ssh', ssh_target, 'hostname', '-I']

        print(f"PLANS - Executing: {hostname_cmd}")
        result = subprocess.run(hostname_cmd, check=True, stdout=subprocess.PIPE, text=True)
        ip_addresses = result.stdout.strip().split()

        # Filter only public IP addresses
        public_ip_addresses = []
        for ip in ip_addresses:
            try:
                ip_obj = ipaddress.ip_address(ip)
                if not ip_obj.is_private:
                    public_ip_addresses.append(ip)
            except ValueError:
                print(f"PLANS - Invalid IP address: {ip}")

        return jsonify({'ip_addresses': public_ip_addresses}), 200

    except subprocess.CalledProcessError as e:
        print(f"PLANS - Internal Server Error")
        abort(500, 'Internal Server Error')
    except Exception as e:
        print(f"PLANS - Unexpected error")
        abort(500, 'Internal Server Error')
