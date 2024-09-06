################################################################################
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
import docker
from flask import Flask, Response, abort, render_template, request, send_file, g, jsonify, session, url_for, flash, redirect, get_flashed_messages
import subprocess
import datetime
import psutil
import docker

# import our modules
from app import app, is_license_valid, login_required_route, load_openpanel_config, connect_to_database
from modules.helpers import get_all_users, get_user_and_plan_count, get_plan_by_id, get_all_plans, get_userdata_by_username, get_hosting_plan_name_by_id, get_user_websites, is_username_unique, gravatar_url

# create plan
@app.route('/plan/new', methods=['POST'])
@login_required_route
def create_plan():
    admin_email = request.form.get('admin_email')
    name = request.form.get('name')
    description = request.form.get('description')
    domains_limit = request.form.get('domains_limit')
    websites_limit = request.form.get('websites_limit')
    disk_limit = request.form.get('disk_limit')
    inodes_limit = request.form.get('inodes_limit')
    db_limit = request.form.get('db_limit')
    cpu = request.form.get('cpu')
    ram = request.form.get('ram')
    docker_image = request.form.get('docker_image')
    bandwidth = request.form.get('port_speed')
    storage_file = request.form.get('storage_file')

    if docker_image == 'openpanel_apache':
        docker_image = 'apache'
    elif docker_image == 'openpanel_nginx':
        docker_image = 'nginx'
    elif docker_image == 'openpanel_litespeed':
        docker_image = 'litespeed'

    command = f"opencli plan-create '{name}' '{description}' {domains_limit} {websites_limit} {disk_limit} {inodes_limit} {db_limit} {cpu} {ram} {docker_image} {bandwidth} {storage_file}"

    try:
        response = subprocess.check_output(command, shell=True, text=True)
        formatted_response = {'message': response}
        return jsonify({'success': True, 'response': formatted_response})

    except subprocess.CalledProcessError as e:
        error_message = f"Error executing command: {e}"
        return jsonify({'success': False, 'error': error_message})



@app.route('/plan/apply/<filename>')
@login_required_route
def serve_log_file(filename):
    log_file_path = f'/tmp/{filename}'
    return send_file(log_file_path, mimetype='text/plain')

# edit plan
@app.route('/plan/edit', methods=['POST'])
@login_required_route
def edit_plan():
    plan_id = request.form.get('id')
    name = request.form.get('name')
    description = request.form.get('description')
    domains_limit = request.form.get('domains_limit')
    websites_limit = request.form.get('websites_limit')
    disk_limit = request.form.get('edit_disk_limit')
    inodes_limit = request.form.get('edit_storage_file_inodes')
    db_limit = request.form.get('edit_db_limit')
    cpu = request.form.get('edit_cpu')
    ram = request.form.get('edit_ram')
    docker_image = request.form.get('edit_docker_image')
    bandwidth = request.form.get('edit_port_speed')
    storage_file = request.form.get('edit_storage_file')

    if docker_image == 'openpanel_apache':
        docker_image = 'apache'
    elif docker_image == 'openpanel_nginx':
        docker_image = 'nginx'
    elif docker_image == 'openpanel_litespeed':
        docker_image = 'litespeed'

    command = f"opencli plan-edit '{plan_id}' '{name}' '{description}' {domains_limit} {websites_limit} {disk_limit} {inodes_limit} {db_limit} {cpu} {ram} {docker_image} {bandwidth} {storage_file}"


    try:
        response = subprocess.check_output(command, shell=True, text=True)
        formatted_response = {'message': response}
        return jsonify({'success': True, 'response': formatted_response})

    except subprocess.CalledProcessError as e:
        error_message = f"Error executing command: {e}"
        return jsonify({'success': False, 'error': error_message})


# delete plan
@app.route('/plan/delete/<plan_name>', methods=['POST'])
@login_required_route
def delete_plan(plan_name):
    command = f"opencli plan-delete '{plan_name}' --json"

    try:
        output = subprocess.check_output(command, shell=True, text=True)
        response_json = json.loads(output)
        return jsonify({'success': True, 'response': response_json})

    except subprocess.CalledProcessError as e:
        error_message = f"Error: {e.output}"
        return jsonify({'success': False, 'error': error_message})


# view plans
@app.route('/plans', defaults={'plan_id': None}, methods=['GET', 'POST'])
@app.route('/plans/<plan_id>', methods=['GET'])
@login_required_route
def plans(plan_id):
    current_route = request.path

    try:
        plans = get_all_plans()

        new_plan_template_path = '/etc/openpanel/openadmin/config/new_plan_template'
        new_plan_template_content = {}
        if os.path.exists(new_plan_template_path):
            with open(new_plan_template_path, 'r') as file:
                new_plan_template_content = json.load(file)

        # json
        output_param = request.args.get('output')
        if output_param == 'json':
            return jsonify({'plans': plans})
        else:
            client = docker.from_env()
            all_images = client.images.list()
            prefix = 'openpanel/'
            filtered_images = [image for image in all_images if any(tag.startswith(prefix) for tag in image.tags)]

            return render_template('plans.html', title='Plans', images=filtered_images, plans=plans, app=app, current_route=current_route, plan_template_data=new_plan_template_content)




    except Exception as e:
        print(f"An error occurred: {e}")

        plans = []
        new_plan_template_content = {}

        output_param = request.args.get('output')
        if output_param == 'json':
            return jsonify({'plans': plans})
        else:
            return render_template('plans.html', title='Plans', plans=plans, app=app, current_route=current_route, plan_template_data=new_plan_template_content)
 
# get server ipv4
@app.route('/system/ips', methods=['GET'])
@login_required_route
def get_ip_addresses():
    try:
        result = subprocess.run(['hostname', '-I'], check=True, stdout=subprocess.PIPE, text=True)
        ip_addresses = result.stdout.strip().split()
        filtered_ip_addresses = [ip for ip in ip_addresses if not (ip.startswith('172.') and 16 <= int(ip.split('.')[1]) <= 31)]
        return jsonify({'ip_addresses': filtered_ip_addresses}), 200
    except subprocess.CalledProcessError as e:
        app.logger.error(f'Error running command: {e}')
        abort(500, 'Internal Server Error')
