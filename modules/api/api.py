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
# Created: 11.07.2023
# Last Modified: 11.06.2024
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




# Python modules
import os
from flask import Flask, Response, render_template, request, g, jsonify, session, redirect, url_for, flash, Blueprint
import subprocess

# Our modules
from app import app, cache, db, load_openpanel_config, admin_required_route


@app.route('/settings/api/endpoints', methods=['GET'])
@admin_required_route
@cache.memoize(timeout=600)
def settings_api_list_endpoints():
    command = "opencli api-list"
    try:
        result = subprocess.check_output(command, shell=True, text=True)
        return Response(result, mimetype='text/plain')  # Return plain text

    except subprocess.CalledProcessError as e:
        return Response(f"Error executing opencli api-list: {e.output}", mimetype='text/plain', status=500)




@app.route('/settings/api', methods=['GET', 'POST'])
@app.route('/settings/api/', methods=['GET', 'POST'])
@admin_required_route
def settings_api():

    if request.method == 'POST':
        # check for ON and OFF only
        action = request.form.get('action').lower()
        # Allow only "on" and "off"
        if action not in ['on', 'off']:
            return jsonify(error="Invalid action."), 400

        command = f"opencli config update api '{action}'"

        try:
            result = subprocess.check_output(command, shell=True, text=True)
            if "Updated api to" in result:
                
                #flash("API setting updated.", 'info')
                #with open("/root/openadmin_restart_needed", "w") as file:
                #    file.write("Restart needed for OpenAdmin service.")
                #

                reload_command = "service admin reload"
                try:
                    subprocess.check_output(reload_command, shell=True, text=True)
                    return redirect('/settings/api')
                except subprocess.CalledProcessError as e_reload:
                    return jsonify(error=f"API was updated, but reloading admin service encountered an error: {e_reload.output}"), 500
                
            else:
                flash("API could not be updated.", 'warning')
        except subprocess.CalledProcessError as e:
            flash(f"Error executing opencli: '{command}': {e.output}", 'error')



               
    view_param = request.args.get('view')
    # return content
    if view_param == 'api_log':
        log_path = '/var/log/openpanel/admin/api.log'
        try:
            with open(log_path, 'r') as log_file:
                log_content = log_file.read()
            return log_content, 200, {'Content-Type': 'text/plain'}
        except IOError as e:
            # If the log file cannot be read, return an appropriate error message
            return f"Unable to read the api log file: {str(e)}", 500, {'Content-Type': 'text/plain'}
    # return the template
    else:
        config_file_path = '/etc/openpanel/openpanel/conf/openpanel.config'
        config_data = load_openpanel_config(config_file_path)

        basic_auth_enabled = config_data.get('PANEL', {}).get('basic_auth', 'no') == 'yes'

        if basic_auth_enabled:
            basic_auth_enabled = "on"
        else:
            basic_auth_enabled = "off"
        api_status_current_value = config_data.get('PANEL', {}).get('api', 'on')


    return render_template('settings/api.html', basic_auth_enabled=basic_auth_enabled, api_status_current_value=api_status_current_value, title='API')
