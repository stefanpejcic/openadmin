################################################################################
# Author: Stefan Pejcic
# Created: 11.07.2023
# Last Modified: 13.03.2024
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
import os
from flask import Flask, Response, abort, render_template, request, send_file, g, jsonify, session, url_for, flash, redirect, get_flashed_messages
import docker
import subprocess
from flask_login import LoginManager, UserMixin, login_user, logout_user, current_user
from flask_sqlalchemy import SQLAlchemy
from werkzeug.security import generate_password_hash, check_password_hash

# import our modules
from app import app, is_license_valid, login_required_route, load_openpanel_config, connect_to_database


# helpers for autologin to user panel!
import random
import re
import string
def generate_random_token(length):
    characters = string.ascii_letters + string.digits
    return ''.join(random.choice(characters) for _ in range(length))

def read_config_file(file_path):
    config = {}
    try:
        with open(file_path, 'r') as file:
            lines = file.readlines()
            for line in lines:
                match = re.match(r'(\w+)=(.*)', line)
                if match:
                    key, value = match.groups()
                    config[key] = value.strip()
    except FileNotFoundError:
        pass
    return config

# autologin admin user to user panel
@app.route('/login/token', methods=['GET'])
@login_required_route
def login_token():
    username = request.args.get('username')
    if not username:
        abort

    token_dir = f'/etc/openpanel/openpanel/core/users/{username}/'
    token_path = f'{token_dir}logintoken.txt'

    if not os.path.exists(token_dir):
        os.makedirs(token_dir)

    random_token = generate_random_token(30)
    with open(token_path, 'w') as file:
        file.write(random_token)

    config_file_path = '/etc/openpanel/openpanel/conf/openpanel.config'
    config = read_config_file(config_file_path)
    port = config.get('port', 2083)

    force_domain = config.get('force_domain')
    force_https = config.get('ssl')
    scheme = 'https' if force_https.lower() == 'yes' else 'http'

    if force_domain:
        hostname = force_domain.strip()
    else:
        output = subprocess.check_output(["hostname", "-I"]).decode("utf-8").strip()
        ips = output.split()
        hostname = ips[0] if ips else "localhost"  

    open_panel = f'{scheme}://{hostname}:{port}'

    open_panel_login_autologin = f'{open_panel}/login_autologin?username={username}&impersonate=yes&admin_token={random_token}'

    response_data = {
        'link': open_panel_login_autologin
    }

    return jsonify(response_data)
