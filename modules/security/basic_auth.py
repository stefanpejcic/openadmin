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
# Created: 14.05.2024
# Last Modified: 14.05.2024
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




import json
from flask import Flask, Response, render_template, request, send_file, jsonify, session, url_for, flash, redirect
import configparser
import os
from app import app, admin_required_route

INI_CONFIG_PATH = '/etc/openpanel/openadmin/config/admin.ini'

class NoSpaceConfigParser(configparser.ConfigParser):
    def write(self, fp, space_around_delimiters=False):  # override default behavior
        if space_around_delimiters:
            super().write(fp, space_around_delimiters=True)
        else:
            for section in self.sections():
                fp.write(f"[{section}]\n")
                for key, value in self.items(section):
                    fp.write(f"{key}={value}\n")
                fp.write("\n")

@app.route('/security/basic_auth', methods=['GET', 'POST'])
@admin_required_route
def basic_auth():
    config = NoSpaceConfigParser()
    config.read(INI_CONFIG_PATH)

    if request.method == 'GET':
        # Read the current SECURITY section
        security = config['SECURITY'] if 'SECURITY' in config else {}
        return render_template(
            'security/basic_auth.html',
            basic_auth=security.get('basic_auth', ''),
            basic_auth_username=security.get('basic_auth_username', ''),
            basic_auth_password=security.get('basic_auth_password', '')
        )

    elif request.method == 'POST':
        data = request.form

        # Update the SECURITY section with provided values
        if 'SECURITY' not in config:
            config.add_section('SECURITY')

        if 'basic_auth' in data:
            config['SECURITY']['basic_auth'] = data['basic_auth']
        if 'basic_auth_username' in data:
            config['SECURITY']['basic_auth_username'] = data['basic_auth_username']
        if 'basic_auth_password' in data:
            config['SECURITY']['basic_auth_password'] = data['basic_auth_password']

        # Save changes back to the file
        try:
            with open(INI_CONFIG_PATH, 'w') as configfile:
                config.write(configfile)

            file_path = '/root/openadmin_restart_needed'
            with open(file_path, 'w') as f:
                f.write("Restart needed") 
            flash('Basic_auth settings for OpenAdmin edited successfully.', 'success')
        except IOError as e:
            flash(f'Failed to write config: {str(e)}', 'error')
        return redirect(url_for('basic_auth'))
