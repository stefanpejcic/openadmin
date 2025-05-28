################################################################################
# *************************************************************************
# *                                                                       *
# * OpenAdmin                                                             *
# * Copyright (c) OpenPanel. All Rights Reserved.                         *
# * Version: 1.3.3                                                        *
# * Build Date: 2025-05-28 10:37:18                                       *
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
# Created: 20.05.2025
# Last Modified: 20.05.2025
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
import os
import json
from flask import Flask, Response, abort, render_template, request, send_file, g, jsonify, session, url_for, flash, redirect, get_flashed_messages
import subprocess
import datetime
import psutil
import threading
# import our modules
from app import app, admin_required_route
from modules.helpers import query_context_by_username


def is_command_safe(command: str) -> bool:
    if re.search(r'[;&|><`$]', command):
        return False
    return True


@app.route('/terminal', methods=['GET', 'POST'])
@admin_required_route
def host_terminal():
    if request.method == 'POST':
        data = request.get_json()
        command = data.get('command', '')
        shell = data.get('shell', 'sh')  # default to sh

        # Allow only 'bash' or 'sh'
        if shell not in ['bash', 'sh']:
            shell = 'sh'

        # if not is_command_safe(command):
        #     return jsonify({'error': 'Unsafe command'}), 400

        try:
            result = subprocess.run(
                [shell, '-c', command],
                stdout=subprocess.PIPE, stderr=subprocess.PIPE, text=True, timeout=10
            )
            output = result.stdout + result.stderr
        except subprocess.TimeoutExpired:
            output = 'Command timed out'
        except Exception as e:
            output = str(e)

        return jsonify({'output': output})

    current_route = request.path
    return render_template('terminal.html', title='Web Terminal', terminal_type='root', current_route=current_route, app=app)


@app.route('/terminal/<username>/<container_name>', methods=['GET', 'POST'])
@admin_required_route
def user_terminal(username, container_name):
    if request.method == 'POST':
        docker_context = query_context_by_username(username)
        data = request.get_json()
        command = data.get('command', '')
        shell = data.get('shell', 'sh')  # default to sh

        # Allow only 'bash' or 'sh'
        if shell not in ['bash', 'sh']:
            shell = 'sh'

        #if not is_command_safe(command):
        #    return jsonify({'error': 'Unsafe command'}), 400

        try:
            result = subprocess.run(
                ['docker', '--context', docker_context, 'exec', '-e', 'TERM=xterm', container_name, shell, '-c', command],
                stdout=subprocess.PIPE, stderr=subprocess.PIPE, text=True, timeout=10
            )
            output = result.stdout + result.stderr
        except subprocess.TimeoutExpired:
            output = 'Command timed out'
        except Exception as e:
            output = str(e)

        return jsonify({'output': output})

    current_route = request.path
    return render_template('terminal.html', title='Docker Terminal', terminal_type='users', username=username, current_route=current_route, app=app, container_name=container_name)
