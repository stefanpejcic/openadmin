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
import json
from flask import Flask, Response, render_template, request, g, jsonify, session, redirect, url_for, flash, send_file, make_response
import subprocess
import datetime
import psutil
from app import app, csrf, cache, is_license_valid, admin_required_route, load_openpanel_config
import modules.helpers
import tempfile

@cache.memoize(timeout=3600)
def is_command_available(command):
    """Check if a command is available on the system."""
    try:
        subprocess.run([command, '--version'], check=True, stdout=subprocess.PIPE, stderr=subprocess.PIPE)
        return True
    except (subprocess.CalledProcessError, FileNotFoundError):
        return False

@app.route('/configservercsf/iframe/', methods=['GET', 'POST'])
@admin_required_route
@csrf.exempt
def configservercsfiframe():
    try:
        if request.method == 'GET':
            qs = '&'.join([f"{key}={value}" for key, value in request.args.items()])
        elif request.method == 'POST':
            qs = '&'.join([f"{key}={value}" for key, value in request.form.items()])

        # FAILED fix for: https://bytetool.web.app/en/ascii/code/0xa9/
        # Unable to create csf UI temp file: 'utf-8' codec can't decode byte 0xa9 in position 1446: invalid start byte
        qs_encoded = qs.encode('utf-8', errors='ignore').decode('utf-8', errors='delete')

        with tempfile.NamedTemporaryFile(mode="w", delete=False, encoding='utf-8') as tmp:
            tmp.write(qs_encoded)
            tmp_name = tmp.name

        command = f"/usr/local/admin/modules/security/csf.pl '{tmp_name}'"

        try:
            # Use subprocess to execute the command
            result = subprocess.run(command, shell=True, capture_output=True, text=True)
            output = result.stdout
        except subprocess.CalledProcessError as e:
            output = f"Output Error from csf UI script: {e}"
        finally:
            if os.path.exists(tmp_name):
                os.unlink(tmp_name)

    except Exception as e:
        output = f"Unable to create csf UI temp file: {e}"

    return make_response(output)


@app.route('/security/firewall', methods=['GET'])
@admin_required_route
def firewall_settings():
    if is_command_available('csf'):
        return render_template('security/csf.html', title="CSF")
    else:
        return "ConfigServer Firewall (CSF) is not available on this system.", 404
