################################################################################
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

login_manager = LoginManager(app)
login_manager.login_view = 'login'

# main logic to login user
class User(db.Model, UserMixin):
    id = db.Column(db.Integer, primary_key=True)
    username = db.Column(db.String(80), unique=True, nullable=False)
    password_hash = db.Column(db.String(120), nullable=False)
    role = db.Column(db.String(50), default='user', nullable=False)
    is_active = db.Column(db.Boolean, default=True, nullable=False)

    def __init__(self, user_id=None, username=None, password=None, is_active=None):
        self.id = user_id
        self.username = username
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



# /login
#
# publically available login page
#
@app.route('/login/', methods=['GET', 'POST'])
@app.route('/login', methods=['GET', 'POST'])
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

                user = User(user_id=user_data.id, username=user_data.username, password=user_data.password_hash, is_active=user_data.is_active)
                login_user(user)
                flash('Login successful', 'success')

                next_page = request.form.get('next') or request.args.get('next')
                if next_page and is_same_domain(request.url_root, next_page):
                    # Extract the path part after the port
                    next_path = urlparse(next_page).path
                    return redirect(next_path)
                else:
                    return redirect(url_for('dashboard'))
        else:
            flash('Login failed. Please check your credentials.', 'danger')

    config_file_path = '/etc/openpanel/openpanel/conf/openpanel.config'
    config_data = load_openpanel_config(config_file_path)
    openpanel_port_current_value = config_data.get('DEFAULT', {}).get('port', '2083')

    return render_template('login.html', port=openpanel_port_current_value)


