################################################################################
# *************************************************************************
# *                                                                       *
# * OpenAdmin                                                             *
# * Copyright (c) OpenPanel. All Rights Reserved.                         *
# * Version: 1.6.0                                                        *
# * Build Date: 2025-09-23 11:18:56                                       *
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
# Last Modified: 26.06.2025
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


# import python modules
from flask import Flask, Response, abort, render_template, request, send_file, g, jsonify, session, url_for, flash, redirect, get_flashed_messages
import psutil
# import our functions
from app import app, admin_required_route
import subprocess, threading, uuid, os

MIGRATE_LOG_PATH = "/tmp/server_migrate.log"
MIGRATE_PROCESS_PID_FILE = "/tmp/server_migrate.pid"



@app.route('/server/migrate', methods=['GET', 'POST'])
@admin_required_route
def server_migrate():
    current_route = request.path
    migrate_started = False

    if request.method == 'POST':
        host = request.form.get('host')
        root = request.form.get('root')
        password = request.form.get('password')

        if not all([host, root, password]):
            print(f"MIGRATE - Aborting process: not all fields are provided: host, root, password")
            flash("All fields are required.", "error")
            return redirect(url_for('server_migrate'))

        # Start migration in background
        with open(MIGRATE_LOG_PATH, 'w') as log_file:
            print(f"MIGRATE - Executing: opencli server-migrate -h {host} --user root --password *******")
            process = subprocess.Popen(
                ['opencli', 'server-migrate', '-h', host, '--user', root, '--password', password],
                stdout=log_file,
                stderr=subprocess.STDOUT,
                bufsize=1,
                universal_newlines=True
            )
            print(f"MIGRATE - Command output is redirected to: {MIGRATE_LOG_PATH}")
            with open(MIGRATE_PROCESS_PID_FILE, 'w') as pid_file:
                pid_file.write(str(process.pid))

        migrate_started = True
        flash("Migration process started.", "success")

    return render_template('server/migrate.html', title='Migrate', route=current_route, migrate_started=migrate_started)


@app.route('/server/migrate/status', methods=['GET'])
@admin_required_route
def server_migrate_status():
    status = 'running'
    output = ''

    if os.path.exists(MIGRATE_LOG_PATH):
        print(f"MIGRATE - Migrate log exists, reading: {MIGRATE_LOG_PATH}")
        with open(MIGRATE_LOG_PATH, 'r') as f:
            output = f.read()

    # Check if process is still running
    if os.path.exists(MIGRATE_PROCESS_PID_FILE):
        print(f"MIGRATE - Migrate process is still running: PID file: {MIGRATE_PROCESS_PID_FILE}")
        with open(MIGRATE_PROCESS_PID_FILE, 'r') as f:
            pid = int(f.read().strip())

        if not psutil.pid_exists(pid):
            status = 'finished'
    else:
        status = 'unknown'

    print(f"MIGRATE - Current status is: {status}")

    return jsonify({'status': status, 'output': output})

