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

# import our modules
from app import app, cache, is_license_valid, login_required_route, connect_to_database
from modules.helpers import get_all_plans, get_plan_by_id

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
        }

        command = f"opencli plan-create name='{form_data['name']}' description='{form_data['description']}' emails={form_data['email_limit']} ftp={form_data['ftp_limit']} domains={form_data['domains_limit']} websites={form_data['websites_limit']} disk={form_data['disk_limit']} inodes={form_data['inodes_limit']} databases={form_data['db_limit']} cpu={form_data['cpu']} ram={form_data['ram']} bandwidth={form_data['bandwidth']}"

        try:
            output = subprocess.run(command, shell=True, text=True, capture_output=True, check=True)
            message = 'Plan created successfully.'
            flash(message, 'success')
            return redirect(url_for('plans'))
        except subprocess.CalledProcessError as e:
            error_message = f"{e.stderr.strip() if e.stderr else e.output.strip()}"
            flash(error_message, 'error')
    
    # GET
    current_route = request.path

    new_plan_template_path = '/etc/openpanel/openadmin/config/new_plan_template'
    new_plan_template_content = {}
    if os.path.exists(new_plan_template_path):
        with open(new_plan_template_path, 'r') as file:
            new_plan_template_content = json.load(file)

    return render_template('new_plan.html', title='Create New Package', app=app, current_route=request.path, form_data=form_data,plan_template_data=new_plan_template_content)




@app.route('/plan/apply/<filename>')
@login_required_route
def serve_log_file(filename):
    log_file_path = f'/tmp/{filename}'
    return send_file(log_file_path, mimetype='text/plain')



# delete plan
@app.route('/plan/delete/<plan_name>', methods=['POST'])
@login_required_route
def delete_plan(plan_name):
    command = f"opencli plan-delete '{plan_name}' --json"

    try:
        output = subprocess.check_output(command, shell=True, text=True)
        message = 'Plan deleted successfully.'
        flash(message, 'success')

    except subprocess.CalledProcessError as e:
        error_message = f"Error: {e.output}"
        flash(error_message, 'error')
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
            f'bandwidth={bandwidth}'
        ]

        try:
            message = subprocess.check_output(command, text=True)
            flash(message, 'success')

        except subprocess.CalledProcessError as e:
            message = f"Error executing command: {e.output}"
            flash(message, 'error')

    # GET
    plan = []
    
    try:
        plan = get_plan_by_id(plan_id)
    except Exception as e:
        print(f"An error occurred: {e}")        

    output_param = request.args.get('output')
    if output_param == 'json':
        return jsonify({'plan': plan})
    else:
        return render_template('edit_plan.html', title=f'Edit plan ID {plan_id}', plan=plan, app=app, current_route=current_route)
 





# view plans
@app.route('/plans', methods=['GET', 'POST'])
@login_required_route
def plans():
    current_route = request.path

    plans = []
    mysql_is_down = False
    
    plans = get_all_plans()
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

@app.route('/system/ips', methods=['GET'])
@login_required_route
@cache.memoize(timeout=3600)
def get_ip_addresses():
    try:
        result = subprocess.run(['hostname', '-I'], check=True, stdout=subprocess.PIPE, text=True)
        ip_addresses = result.stdout.strip().split()

        public_ip_addresses = []
        for ip in ip_addresses:
            try:
                ip_obj = ipaddress.ip_address(ip)
                if not ip_obj.is_private:  # Only add public IPs
                    public_ip_addresses.append(ip)
            except ValueError:
                app.logger.warning(f"Invalid IP address: {ip}")
        
        return jsonify({'ip_addresses': public_ip_addresses}), 200
        ''' 
        output_param = request.args.get('output')
        if output_param == 'json':
            return jsonify({'ip_addresses': public_ip_addresses}), 200
        else:
            current_route = request.path
            return render_template('plans_ips.html', title='Plans', current_route=current_route)
        '''
    except subprocess.CalledProcessError as e:
        abort(500, 'Internal Server Error')
