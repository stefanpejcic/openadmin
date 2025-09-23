################################################################################
# *************************************************************************
# *                                                                       *
# * OpenAdmin                                                             *
# * Copyright (c) OpenPanel. All Rights Reserved.                         *
# * Version: 1.6.0                                                        *
# * Build Date: 2025-09-23 11:25:17                                       *
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
import re
import json
import socket
from flask import Flask, Response, abort, render_template, request, send_file, g, jsonify, session, url_for, flash, redirect, get_flashed_messages
import subprocess
import psutil
from app import app, cache, admin_required_route, load_openpanel_config
from modules.services.logs import get_op_update_logs
import requests



@cache.memoize(timeout=7200)
def fetch_docker_tags():
    url = "https://hub.docker.com/v2/repositories/openpanel/openpanel-ui/tags?page_size=100"
    print(f"UPDATES - Checking latest OpenPanel version from: {url} (data is cached for 7200s)")
    response = requests.get(url)
    response.raise_for_status()
    tags = [tag['name'] for tag in response.json().get('results', [])]
    return sorted([t for t in tags if t != "latest"], reverse=True)

@app.route('/api/docker-tags', methods=['GET', 'POST'])
@admin_required_route
def get_docker_tags():
    if request.method == 'GET':
        try:
            return jsonify(fetch_docker_tags())
        except Exception as e:
            return jsonify({"error": str(e)}), 500

    elif request.method == 'POST':
        try:
            data = request.form or request.get_json()
            version = data.get('version')
            if not version:
                print(f"UPDATES - Changing tag failed: version not provided.")
                flash("Version not provided", "error")
                return redirect(url_for('up_update_settings'))

            env_path = "/root/.env"
            updated = False

            print(f"UPDATES - Changing tag in {env_path} to: {version}")
            if os.path.exists(env_path):
                with open(env_path, 'r') as f:
                    lines = f.readlines()
                with open(env_path, 'w') as f:
                    for line in lines:
                        if line.startswith("VERSION="):
                            f.write(f'VERSION="{version}"\n')
                            updated = True
                        else:
                            f.write(line)
                    if not updated:
                        f.write(f'VERSION="{version}"\n')
            else:
                with open(env_path, 'w') as f:
                    f.write(f'VERSION="{version}"\n')

            print(f"UPDATES - Pulling image: openpanel/openpanel-ui:{version}")
            subprocess.check_call(["docker", "pull", f"openpanel/openpanel-ui:{version}"], cwd="/root")
            print(f"UPDATES - Starting OpenPanel with the new image tag..")
            subprocess.check_call(["docker", "compose", "up", "-d", "openpanel"], cwd="/root")

            flash(f"Downgraded to version {version} successfully.", "success")
            return redirect(url_for('up_update_settings'))

        except subprocess.CalledProcessError as e:
            print(f"UPDATES - Command failed: {e}")
            flash(f"Command failed: {e}", "error")
            return redirect(url_for('up_update_settings'))

        except Exception as e:
            print(f"UPDATES - Exception: {e}")
            flash(str(e), "error")
            return redirect(url_for('up_update_settings'))

@cache.memoize(timeout=7200)
def get_latest_version():
    image_name = 'openpanel/openpanel-ui'  # TODO: edit to check .env file!
    docker_url = f"https://hub.docker.com/v2/repositories/{image_name}/tags"
    fallback_url = "https://usage-api.openpanel.org/latest_version"

    try:
        print(f"UPDATES - Checking latest OpenPanel version published on: {docker_url}")
        response = requests.get(docker_url, timeout=5)
        response.raise_for_status()
        tags = response.json().get('results', [])
        
        versions = [
            tag['name'] for tag in tags 
            if tag['name'] != 'latest' and tag['name'].replace('.', '').isdigit()
        ]
        
        if versions:
            return sorted(versions, key=lambda v: list(map(int, v.split('.'))))[-1]
    except requests.RequestException as e:
        print(f"UPDATES - Error fetching Docker Hub tags: {e}")

    # Fallback to usage API
    try:
        print(f"UPDATES - Checking latest OpenPanel version published on: {fallback_url}")
        fallback_resp = requests.get(fallback_url, timeout=5)
        fallback_resp.raise_for_status()
        data = fallback_resp.json()
        return data.get("latest_version")
    except requests.RequestException as e:
        print(f"UPDATES - Error fetching fallback version: {e}")
        return "0.0.0"

@app.route('/settings/updates/update_now', methods=['POST'])
@admin_required_route
def update_now():
    try:
        cmd = ['timeout', '600s', 'opencli', 'update', '--force']
        print(f"UPDATES - Executing: {' '.join(cmd)}")
        subprocess.Popen(cmd, start_new_session=True)
        flash('Update process started successfully.', 'info')
    except Exception as e:
        flash(f'Error: Failed to start the update process. Details: {str(e)}', 'error')
    return redirect(url_for('up_update_settings'))




@app.route('/settings/updates', methods=['GET', 'POST'])
@admin_required_route
def up_update_settings():

    config_file = '/etc/openpanel/openpanel/conf/openpanel.config'

    if request.method == 'POST':
        preference = request.form.get('preference')

        updates = {
            'minor_and_major': {'autoupdate': 'on', 'autopatch': 'on'},
            'minor_only': {'autoupdate': 'off', 'autopatch': 'on'},
            'major_only': {'autoupdate': 'on', 'autopatch': 'off'},
            'none': {'autoupdate': 'off', 'autopatch': 'off'}
        }.get(preference)

        print(f"UPDATES - Reading: {config_file}")
        with open(config_file, "r") as file:
            content = file.read()

        for key, value in updates.items():
            content = content.replace(f'{key}=on', f'{key}={value}').replace(f'{key}=off', f'{key}={value}')

        print(f"UPDATES - Writing to: {config_file}")
        with open(config_file, "w") as file:
            file.write(content)

        with open('/root/openpanel_restart_needed', 'w') as f:
            f.write("Restart needed")

        print(f"UPDATES - Deleting cached configuration..")
        cache.delete_memoized(load_openpanel_config)


    # read after update
    config_data = load_openpanel_config(config_file)
    current_route = request.path
    latest_version = get_latest_version()
    update_logs = get_op_update_logs()
    
    return render_template('settings/updates.html', title='Update Settings', current_route=current_route, app=app, config_data=config_data, latest_version=latest_version, update_logs=update_logs)
