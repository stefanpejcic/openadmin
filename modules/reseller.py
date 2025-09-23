import os
# *************************************************************************
# *                                                                       *
# * OpenAdmin                                                             *
# * Copyright (c) OpenPanel. All Rights Reserved.                         *
# * Version: 1.6.0                                                        *
# * Build Date: 2025-09-23 11:24:39                                       *
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
import json
import subprocess
from flask import Flask, Response, abort, render_template, request, send_file, g, jsonify, session, url_for, flash, redirect, get_flashed_messages
from app import app, cache, is_license_valid, load_openpanel_config, connect_to_database
import docker
from urllib.parse import unquote
from functools import wraps
from flask_login import LoginManager, UserMixin, login_user, logout_user, current_user

# runs on import!
resellers_dir = "/etc/openpanel/openadmin/resellers"
if not os.path.exists(resellers_dir):
        print(f"RESELLER - Creating: {resellers_dir}")
        os.makedirs(resellers_dir)

admin_config_file_path = '/etc/openpanel/openadmin/config/admin.ini'
admin_config_data = load_openpanel_config(admin_config_file_path)


def reseller_required(func):
    @wraps(func)
    def wrapper(*args, **kwargs):
        print(f"RESELLER - Checking if user is a Reseller and resellers are enabled..")
        if not current_user.is_authenticated:
            print(f"RESELLER - Aborting check: user is not authenticated!")
            return redirect(url_for('login', next=request.url))
        
        # Check if the current user's role is user (reseller)
        if getattr(current_user, 'role', None) != 'reseller':
            print(f"RESELLER - Aborting check: user is not a reseller!")
            abort(403)  # Forbidden
        reseller_mode = admin_config_data.get('USERS', {}).get('reseller', 'no').lower()
        if reseller_mode.lower() != "yes": # off for versions <0.3.7
            print(f"RESELLER - Aborting check: reseller mode is disbaled. Enable it with: 'opencli config update reseller yes'")
            flash("Access denied: Reseller feature is disabled.", "danger")
            return redirect(url_for('login', next=request.url))
        return func(*args, **kwargs)
    wrapper.__reseller_required__ = True
    return wrapper


@app.route('/reseller')
@app.route('/reseller/dashboard')
@reseller_required
def reseller_dashboard():
    current_route = request.path
    return render_template('reseller/dashboard.html', title='Reseller Dashboard', app=app, current_route = current_route)
