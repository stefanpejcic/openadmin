################################################################################
# *************************************************************************
# *                                                                       *
# * OpenAdmin                                                             *
# * Copyright (c) OpenPanel. All Rights Reserved.                         *
# * Version: 1.6.0                                                        *
# * Build Date: 2025-09-23 11:17:39                                       *
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
# Last Modified: 10.06.2024
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
import shutil
import socket
import json
import mimetypes
from flask import Flask, Response, render_template, request, g, jsonify, session, redirect, url_for, flash, send_file, send_from_directory, make_response
import subprocess
import datetime
import psutil
from app import app, csrf, cache, is_license_valid, admin_required_route, load_openpanel_config
import requests

PHP_ROOT = '/etc/sysconfig/imunify360/'

def run_php_script_proxy(script_path):
    target_url = f"http://localhost:9000{request.path}"
    print(f"SECURITY.IMUNIFY - Fetching: {target_url}")
    resp = requests.request(
        method=request.method,
        url=target_url,
        headers={k: v for k, v in request.headers},
        data=request.get_data(),
        params=request.args
    )
    return resp.content, resp.status_code, resp.headers.items()


@app.route('/security/imunify/assets/static/<path:filename>')
@admin_required_route
@csrf.exempt
def imunifyav_static_files(filename):
    directory = '/etc/sysconfig/imunify360/imav/assets/static'
    file_path = os.path.join(directory, filename)

    if not os.path.isfile(file_path):
        return abort(404)

    mime_type, _ = mimetypes.guess_type(file_path)
    if not mime_type:
        mime_type = 'application/octet-stream'

    response = send_from_directory(directory, filename, conditional=True)

    # Only override Content-Type if needed (preferably, don't override)
    if response.content_type != mime_type:
        response.headers['Content-Type'] = mime_type

    return response


# check if php service is running on :9000
def is_port_open(host, port):
    print(f"SECURITY.IMUNIFY - Checking if PHP service is running on: {host}:{port}")
    with socket.socket(socket.AF_INET, socket.SOCK_STREAM) as sock:
        sock.settimeout(1)
        result = sock.connect_ex((host, port))
        return result == 0


def start_imunify_detached():
    # https://github.com/stefanpejcic/OpenPanel/issues/685
    cmd = ['opencli', 'imunify', 'start']
    print(f"SECURITY.IMUNIFY - Executing: opencli imunify start")
    proc = subprocess.Popen(cmd, stdout=subprocess.PIPE, stderr=subprocess.PIPE)
    out, err = proc.communicate()
    print("stdout:", out.decode())
    print("stderr:", err.decode())



# autologin!
def get_imunify_token():
    try:
        print(f"SECURITY.IMUNIFY - Executing: imunify360-agent login get --username root")
        result = subprocess.run(
            ['imunify360-agent', 'login', 'get', '--username', 'root'],
            stdout=subprocess.PIPE, stderr=subprocess.PIPE, text=True
        )
        
        if result.returncode == 0:
            token = result.stdout.strip()
            return token
        else:
            print(f"SECURITY.IMUNIFY - Error: {result.stderr}")
            return None
    except Exception as e:
        print(f"SECURITY.IMUNIFY - Exception occurred: {e}")
        return None


@app.route('/security/imunify/')
@admin_required_route
def imunifyav_gui():
    if shutil.which('imunify360-agent') is None:
        return render_template('security/imunify_not_installed.html', title="ImunifyAV (Not Installed)"), 200

    if not is_port_open('127.0.0.1', 9000):
        start_imunify_detached()
        return render_template('security/imunify_not_running.html', title="ImunifyAV (Not Running)"), 200

    token = get_imunify_token()
    
    '''
    auth_admin = []
    try:
        with open('/etc/sysconfig/imunify360/auth.admin', 'r') as f:
            auth_admin = [line.strip() for line in f if line.strip()]
    except Exception as e:
        print(f"Error reading auth.admin file: {e}")
        auth_admin = 'root'

    return render_template('security/imunify_is_installed_and_running.html', auth_admin=auth_admin), 200
    '''

    if token:
        return render_template('security/imunify.html', title="ImunifyAV", token=token)
    else:
        flash('Failed to generate token for auto-login to ImunifyAV. Please use SSH user and password to login.', 'warning')
        return render_template('security/imunify.html', title="ImunifyAV", token=None)


@app.route('/imav/', defaults={'path': 'index.php'}, methods=['GET', 'POST'])
@app.route('/imav/<path:path>', methods=['GET', 'POST'])
@csrf.exempt
@admin_required_route
def imunifyav_php_handler(path):
    if shutil.which('imunify360-agent') is None:
        return render_template('security/imunify_not_installed.html'), 200

    if not is_port_open('127.0.0.1', 9000):
        start_imunify_detached()
        return render_template('security/imunify_not_running.html'), 200

    safe_path = os.path.normpath(path)
    if safe_path.startswith('..'):
        print(f"SECURITY.IMUNIFY - Path is forbidden!")
        return "Forbidden", 403

    script_path = os.path.join(PHP_ROOT, safe_path)

    return run_php_script_proxy(script_path)
