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
import re
import subprocess

# import our functions
from app import app, cache, is_license_valid, admin_required_route, load_openpanel_config, connect_to_database
#from modules.users import read_env_values

def read_env_values():
    env_path = '/etc/openpanel/docker/compose/1.0/.env'
    values = {
        'DEFAULT_PHP_VERSION': None,
        'MYSQL_TYPE': None,
        'WEB_SERVER': None,
        'VARNISH': None
    }
    try:
        with open(env_path, 'r') as file:
            for line in file:
                line = line.strip()
                if line.startswith('MYSQL_TYPE='):
                    values['MYSQL_TYPE'] = line.split('=', 1)[1].strip().strip('"').strip("'")
                elif line.startswith('WEB_SERVER='):
                    values['WEB_SERVER'] = line.split('=', 1)[1].strip().strip('"').strip("'")
                elif line.startswith('DEFAULT_PHP_VERSION='):
                    values['DEFAULT_PHP_VERSION'] = line.split('=', 1)[1].strip().strip('"').strip("'")
                elif line.startswith('#PROXY_HTTP_PORT='):
                    values['VARNISH'] = False
                elif line.startswith('PROXY_HTTP_PORT='):
                    values['VARNISH'] = True
    except FileNotFoundError:
        return None
    return values


@app.route('/server/defaults', methods=['GET', 'POST'])
@admin_required_route
def edit_defaults_for_new_users():
    current_route = request.path
    
    #if request.method == 'POST':
        

    defaults = read_env_values()


    if request.args.get('output') == 'json':
        return jsonify(defaults)
    return render_template('server/defaults.html', title='Edit defaults', current_route=current_route, defaults=defaults)
