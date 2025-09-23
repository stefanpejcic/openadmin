################################################################################
# *************************************************************************
# *                                                                       *
# * OpenAdmin                                                             *
# * Copyright (c) OpenPanel. All Rights Reserved.                         *
# * Version: 1.6.0                                                        *
# * Build Date: 2025-09-23 11:22:08                                       *
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
# Created: 25.04.2024
# Last Modified: 03.05.2024
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
from flask import Flask, Response, abort, render_template, request, send_file, g, jsonify, session, url_for, flash, redirect, get_flashed_messages, render_template_string

import requests
import os
import tempfile
import re
import subprocess

# import our functions
from app import app, cache, is_license_valid, admin_required_route, load_openpanel_config, connect_to_database
#from modules.users import read_env_values

global_env_path = '/root/.env'

def read_env_values():
    print(f"LIMITS - Reading variables from: {global_env_path}")

    grouped_values = {'DEFAULTS': {}}

    exclude_keys = {
        'VERSION', 'PORT'
    }

    try:
        with open(global_env_path, 'r') as file:
            for line in file:
                raw_line = line
                line = line.strip()
                if not line or line.startswith('#') or '=' not in line:
                    continue

                key, value = line.split('=', 1)
                key = key.strip()
                value = value.strip().strip('"').strip("'")

                if key in exclude_keys or (key.endswith('_PORT') and key != 'PROXY_HTTP_PORT') or key.endswith('_PW') or key.endswith('_PASSWORD') or key.endswith('_USER'):
                    continue

                # Generic key grouping
                parts = key.split('_', 1)
                prefix, suffix = parts if len(parts) == 2 else (parts[0], '')
                if prefix not in grouped_values:
                    grouped_values[prefix] = {}
                grouped_values[prefix][suffix] = value

    except FileNotFoundError:
        return None

    return grouped_values



@app.route('/services/limits', methods=['GET', 'POST'])
@admin_required_route
def edit_limits_for_services():
    current_route = request.path
    
    if request.method == 'POST':
        try:
            print(f"LIMITS - Reading data from: {global_env_path}")
            with open(global_env_path, 'r') as file:
                lines = file.readlines()
        except FileNotFoundError:
            print(f"LIMITS - File not found: {global_env_path}")
            flash("Environment file not found.", "error")
            return redirect(request.url)

        new_lines = []
        varnish_enabled = request.form.get('VARNISH', '').strip() == '1'

        for line in lines:
            stripped = line.strip()
            # Skip lines that don't look like key=value
            if not stripped or stripped.startswith('#') or '=' not in line:
                new_lines.append(line)
                continue

            key, old_value = line.split('=', 1)
            key = key.strip()
            old_value = old_value.strip()
            form_key = key

            if form_key in request.form:
                new_value = request.form[form_key].strip()

                if key.endswith('_RAM') and new_value.isdigit() and new_value != '0':
                    new_value = f"{new_value}G"

                new_value = f'"{new_value}"'

                new_lines.append(f"{key}={new_value}\n")

            else:
                new_lines.append(line)

        final_lines = []
        for line in new_lines:
            final_lines.append(line)

        try:
            print(f"LIMITS - Saving limits..")
            with open(global_env_path, 'w') as file:
                file.writelines(final_lines)
            flash(f"New limits saved successfully!", "success")

        except Exception as e:
            print(f"LIMITS - Failed to update limits: {str(e)}")
            flash(f"Failed to update limits: {str(e)}", "error")

    defaults = read_env_values()

    if request.args.get('output') == 'json':
        return jsonify(defaults)
    return render_template('services/limits.html', title='Servie Limits', current_route=current_route, defaults=defaults)

