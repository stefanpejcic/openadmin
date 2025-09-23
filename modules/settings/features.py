################################################################################
# *************************************************************************
# *                                                                       *
# * OpenAdmin                                                             *
# * Copyright (c) OpenPanel. All Rights Reserved.                         *
# * Version: 1.6.0                                                        *
# * Build Date: 2025-09-23 11:28:24                                       *
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
# Last Modified: 21.10.2024
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

import re
import os
import json
import socket
from flask import Flask, Response, abort, render_template, request, send_file, g, jsonify, session, url_for, flash, redirect, get_flashed_messages
import subprocess
import datetime
import psutil
from app import app, cache, is_license_valid, admin_required_route, connect_to_database
import glob

from modules.settings.modules import get_all_plugins

def check_if_feature_in_use(feature_name):
    print(f"FEATURES - Checking if feature_set: {feature_name} is used on any hosting plan..")
    conn = connect_to_database()
    cursor = conn.cursor()
    query = """
        SELECT 1
        FROM plans
        WHERE feature_set = %s
        LIMIT 1;
    """
    cursor.execute(query, (feature_name,))
    result = cursor.fetchone()
    cursor.close()
    conn.close()
    return result is not None

@app.route('/features/', defaults={'plan': ''}, methods=['GET', 'POST'])
@app.route('/features/<plan>', methods=['GET', 'POST'])
@admin_required_route
def open_panel_enable_features(plan):
    features_dir = '/etc/openpanel/openpanel/features/'

    if not plan:
        if request.method == 'POST':
            feature_name = request.form.get('feature_name')
            if feature_name:
                file_path = os.path.join(features_dir, f"{feature_name}.txt")                
                if not os.path.exists(file_path):
                    print(f"FEATURES - Creating feature_set file: {file_path}")
                    with open(file_path, 'w') as f:
                        f.write('')
                    flash('Feature set created successfully.', 'success')
                else:
                    print(f"FEATURES - Warning creating feature_set file - already exists: {file_path}")
                    flash('Feature set already already exists.', 'warning')
            else:
                print(f"FEATURES - Aborted creating feature_set file: feature_name is not provided!")
                flash('Name for feature set is required.', 'danger')
            return redirect(url_for('open_panel_enable_features'))
        else:
            print(f"FEATURES - Reading feature_sets from: {features_dir} directory.")
            try:
                files = [f[:-4] for f in os.listdir(features_dir) if f.endswith('.txt')]
            except FileNotFoundError:
                files = []
            if request.args.get('output') == 'json':
                return jsonify(files)
            return render_template('settings/features.html', title='Select a Plan', files=files)
     
    config_file_path = os.path.join(features_dir, f'{plan}.txt')
    features_json_path = '/etc/openpanel/openadmin/config/features.json'

    if request.method == 'POST':
        action = request.form.get('action').lower()
        if action not in ['enable_all', 'disable_all', 'update', 'delete']:
            print(f"FEATURES - Aborted action: {action} - invalid, only permited are: enable_all, disbale_all, update, delete.")
            flash('Invalid action.', 'danger')
            return redirect(f'/features/{plan}')
        if action == 'update':
            print(f"FEATURES - Updating features in file: {config_file_path}")
            enabled_modules = [key for key in request.form.keys() if key != 'csrf_token']
            with open(config_file_path, 'w') as file:
                for feature in enabled_modules:
                    file.write(f"{feature}\n")
            flash('Features updated successfully.', 'success')

        elif action == 'disable_all':
            print(f"FEATURES - Disabling all features in file: {config_file_path}")
            with open(config_file_path, 'w') as file:
                file.write('')
            flash('All features disabled successfully.', 'success')
        elif action == 'enable_all':
            print(f"FEATURES - Enabling all features in file: {config_file_path}")
            try:
                with open(config_file_path, 'r') as file:
                    enabled_modules = [line.strip() for line in file if line.strip()]
            except FileNotFoundError:
                enabled_modules = []

            with open(features_json_path, 'r') as f:
                all_features = json.load(f)

            with open(config_file_path, 'w') as file:
                for feature in all_features:
                    file.write(feature['name'] + '\n')

            flash('All features enabled successfully.', 'success')

        elif action == 'delete':
            if plan == 'default':
                print(f"FEATURES - Aborting delete of feature_set: 'default' can not be deleted!")
                flash(f'Error: default features set can not be deleted.', 'error')
                return redirect('/features/default')
            if check_if_feature_in_use(plan):
                print(f"FEATURES - Aborting delete of feature_set: {plan} - it is in use by a hosting plan.")
                flash(f'Error: feature set {plan} can not be deleted as it is used by a hosting package.', 'error')
                return redirect(f'/features/{plan}')
            if os.path.exists(config_file_path):
                print(f"FEATURES - Deleted feature_set file: {config_file_path}")
                os.remove(config_file_path)
            flash(f'features set {plan} deleted successfully.', 'success')
            return redirect('/features/')

        # Mark OpenPanel for restart
        print(f"FEATURES - Creating restart needed file for OpenPanel.")
        with open("/root/openpanel_restart_needed", "w") as file:
            file.write("Restart needed for OpenPanel service.")


    current_route = request.path
    enabled_modules = []

    print(f"FEATURES - Reading all features from: {config_file_path}")
    try:
        with open(config_file_path, 'r') as file:
            enabled_modules = [line.strip() for line in file if line.strip()]
    except FileNotFoundError:
        enabled_modules = []


    plugins = get_all_plugins()

    # Load feature definitions
    with open(features_json_path, 'r') as f:
        all_features = json.load(f)

    print(f"FEATURES - Checking statuses for modules..")
    for feature in all_features:
        feature['status'] = feature['name'] in enabled_modules
    print(f"FEATURES - Checking statuses for plugins..")
    for plugin in plugins:
        plugin['status'] = plugin['name'] in enabled_modules

    # json
    output_param = request.args.get('output')
    if output_param == 'json':
        return jsonify(
            enabled_modules=enabled_modules,
            features=all_features,
            plugins=plugins,
        )

    return render_template('settings/features.html', title='Manage Plan Features',
                           current_route=current_route,
                           enabled_modules=enabled_modules,
                           plan=plan,
                           app=app,
                           features=all_features,
                           plugins=plugins)
