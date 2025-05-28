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




# import python modules
import datetime
from flask import Flask, Response, abort, render_template, request, send_file, g, jsonify, session, url_for, flash, redirect, get_flashed_messages
from flask_login import LoginManager, UserMixin, login_user, logout_user, current_user
from werkzeug.security import generate_password_hash, check_password_hash
from urllib.parse import urlparse

# import our modules
from app import app, db, is_license_valid, login_required_route, load_openpanel_config, connect_to_database

import os
import subprocess

from flask_limiter import Limiter
from flask_limiter.util import get_remote_address
from flask_limiter.errors import RateLimitExceeded


# Initialize Flask-Limiter
limiter = Limiter(
    get_remote_address,
    app=app
)


login_manager = LoginManager(app)
login_manager.login_view = 'login'

# main logic to login user
class User(db.Model, UserMixin):
    id = db.Column(db.Integer, primary_key=True)
    username = db.Column(db.String(80), unique=True, nullable=False)
    password_hash = db.Column(db.String(120), nullable=False)
    role = db.Column(db.String(50), default='user', nullable=False)
    is_active = db.Column(db.Boolean, default=True, nullable=False)

    def __init__(self, user_id=None, username=None, password=None, role=None, is_active=None):
        self.id = user_id
        self.username = username
        self.role = role
        self.set_password(password)
        self.is_active = is_active
    def set_password(self, password):
        self.password_hash = generate_password_hash(password)
        
    def check_password(self, password):
        return check_password_hash(self.password_hash, password)
    
    def get_id(self):
        return str(self.id)

@login_manager.user_loader
def load_user(user_id):
    return User.query.get(int(user_id))


# avoid redirects to 3rd party sites
def is_same_domain(url1, url2):
    parsed_url1 = urlparse(url1)
    parsed_url2 = urlparse(url2)
    return (parsed_url1.scheme, parsed_url1.netloc) == (parsed_url2.scheme, parsed_url2.netloc)


######## rate limiting added in 0.3.5
config_file_path = '/etc/openpanel/openadmin/config/admin.ini'
config_data = load_openpanel_config(config_file_path)
#post_login_limit = config_data.get('DEFAULT', {}).get('admin_login_ratelimit', '5 per minute')
post_login_limit_from_file = int(config_data.get('PANEL', {}).get('login_ratelimit', 5))
post_login_limit = f"{post_login_limit_from_file} per minute"
post_block_limit = config_data.get('PANEL', {}).get('login_blocklimit', 20)
post_block_limit = int(config_data.get('PANEL', {}).get('login_blocklimit', 20))


# in memory so that admin restart clears it!
failed_attempts = {}

def block_ip_temporarily(ip, post_block_limit):
    try:
        subprocess.run("csf -v", shell=True, check=True)
        command = f"csf -td {ip} 'Too many failed login attempts on OpenAdmin'"
        subprocess.run(command, shell=True, check=True)
        log_file_path = '/var/log/openpanel/admin/failed_login.log'
        current_time = datetime.datetime.now().strftime("%Y-%m-%d %H:%M:%S")
        with open(log_file_path, 'a') as log_file:
            log_file.write(f"{current_time} IP: {ip} temporary blocked on CSF due to {post_block_limit} failed logins.\n")
    except subprocess.CalledProcessError:
        with open('/var/log/openpanel/admin/error.log', 'a') as error_log:
            error_log.write(f"{datetime.datetime.now()} - Failed to block IP {ip} on Firewall: {str(e)}\n")
    except Exception as e:
        # Handle any other exceptions
        with open('/var/log/openpanel/admin/error.log', 'a') as error_log:
            error_log.write(f"{datetime.datetime.now()} - An unexpected error occurred: {str(e)}\n")





@app.errorhandler(RateLimitExceeded)
def handle_rate_limit_error(e):
    # Log the error to a file
    log_file_path = '/var/log/openpanel/admin/failed_login.log'
    if not os.path.exists(log_file_path):
        with open(log_file_path, 'w'):
            pass
    current_time = datetime.datetime.now().strftime("%Y-%m-%d %H:%M:%S")
    user_ip = request.remote_addr
    with open(log_file_path, 'a') as log_file:
        log_file.write(f"{current_time} Rate limit for login: '{post_login_limit}' exceeded from IP: {user_ip}\n")

    if user_ip in failed_attempts:
        failed_attempts[user_ip] += 1
    else:
        failed_attempts[user_ip] = 1

    if failed_attempts[user_ip] > post_block_limit:
        block_ip_temporarily(user_ip, post_block_limit)

    # Flash a message to the user
    flash('Too many failed login attempts. Please try again later.', 'danger')
    return redirect(url_for('login'))





# /login
#
# publically available login page
#
@app.route('/login/', methods=['GET', 'POST'])
@app.route('/login', methods=['GET', 'POST'])
@limiter.limit(post_login_limit, methods=["POST"])
def login():

    # POST:
    if request.method == 'POST':
        username = request.form['username']
        password = request.form['password']
        user_ip = request.remote_addr 

        user_data = User.query.filter_by(username=username).first()

        if user_data and user_data.check_password(password):
            if not user_data.is_active:
                flash('Login failed. User is not active.', 'danger')
            else:
                # Log the successful login to a file
                log_file_path = '/var/log/openpanel/admin/login.log'
                current_time = datetime.datetime.now().strftime("%Y-%m-%d %H:%M:%S")
                with open(log_file_path, 'a') as log_file:
                    log_file.write(f"{current_time} {username} {user_ip}\n")

                user = User(user_id=user_data.id, username=user_data.username, password=user_data.password_hash, role=user_data.role ,is_active=user_data.is_active)
                login_user(user)

                if user_ip in failed_attempts:
                    del failed_attempts[user_ip]

                if user_data.role == 'reseller':
                    flash('Login successful', 'success')
                    return redirect('/users')  
                
                if user_data.role == 'admin' or user_data.role == 'user':
                    next_page = request.form.get('next') or request.args.get('next')
                    if next_page and is_same_domain(request.url_root, next_page):
                        next_path = urlparse(next_page).path
                        return redirect(next_path)

                    else: # fallback!
                        return redirect(url_for('dashboard'))
        else:
            flash('Login failed. Please check your credentials.', 'danger')

    config_file_path = '/etc/openpanel/openpanel/conf/openpanel.config'
    config_data = load_openpanel_config(config_file_path)
    openpanel_port_current_value = config_data.get('DEFAULT', {}).get('port', '2083')

    return render_template('login.html', port=openpanel_port_current_value)


