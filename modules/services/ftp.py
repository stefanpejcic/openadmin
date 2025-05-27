################################################################################
# *************************************************************************
# *                                                                       *
# * OpenAdmin                                                             *
# * Copyright (c) OpenPanel. All Rights Reserved.                         *
# * Version: 1.3.2                                                        *
# * Build Date: 2025-05-27 19:36:19                                       *
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
# Created: 10.09.2024
# Last Modified: 10.09.2024
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


import traceback

# import python modules
import os
import json
import docker
from flask import Flask, Response, abort, render_template, request, send_file, g, jsonify, session, url_for, flash, redirect, get_flashed_messages
import subprocess
import datetime
import psutil

# import our modules
from app import app, cache, admin_required_route, connect_to_database
from modules.helpers import get_all_ftp_accounts


# on every route we need to first check if ftp is running!
def check_ftpserver_status():
    try:
        # First, check if the compose.yml file exists
        all_users_file = '/etc/openpanel/ftp/all.users'
        if not os.path.exists(all_users_file) or os.path.getsize(all_users_file) == 0:
            return 'not_installed'

        # Run the 'docker ps' command to check if the container is running
        result = subprocess.run(
            ['docker', 'ps', '--filter', 'name=openadmin_ftp', '--filter', 'status=running', '--format', '{{.Names}}'],
            stdout=subprocess.PIPE,
            stderr=subprocess.PIPE,
            text=True
        )
        # Check if the container name appears in the output
        if 'openadmin_ftp' in result.stdout:
            return 'running'
        else:
            return 'stopped'
    except Exception as e:
        print(f"Failed to check FTP server status: {e}")
        return 'unknown'



@app.route('/services/ftp/refresh', methods=['GET', 'POST'])
@admin_required_route
def ftp_refresh():
    command = "opencli ftp-users"
    try:
        result = subprocess.check_output(command, shell=True, text=True)
        return Response(result, mimetype='text/plain')  # Return plain text

    except subprocess.CalledProcessError as e:
        return Response(f"Error executing opencli ftp-users: {e.output}", mimetype='text/plain', status=500)




@app.route('/services/ftp', methods=['GET', 'POST'])
@admin_required_route
def ftp():
    current_route = request.path
    output_param = request.args.get('output')  # json

    try:

        ftpserver_status = check_ftpserver_status()

        if ftpserver_status != 'running':
            if output_param == 'json':
                return jsonify({'status': ftpserver_status})
            return render_template(
                'services/ftp.html',
                ftpserver_status=ftpserver_status,
                app=app,
                current_route=current_route,
                title="FTP"
            )

        ftp_accounts = get_all_ftp_accounts()

        if output_param == 'json':
            return jsonify({'ftpserver_status': ftpserver_status,'ftp_accounts': ftp_accounts})
        return render_template(
            'services/ftp.html',
            ftpserver_status=ftpserver_status,
            ftp_accounts=ftp_accounts,
            app=app,
            current_route=current_route,
            title="FTP"
        )

    except Exception as e:
        # Print detailed exception and stack trace
        print(f"An error occurred: {e}")
        print(traceback.format_exc())

        # Return a response indicating an error
        ftp_accounts = []
        if output_param == 'json':
            return jsonify({'status': ftpserver_status, 'accounts': ftp_accounts})
        return render_template(
            'services/ftp.html',
            ftpserver_status=ftpserver_status,
            ftp_accounts=ftp_accounts,
            app=app,
            current_route=current_route,
            title="FTP"
        )


