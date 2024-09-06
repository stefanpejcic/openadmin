################################################################################
# Author: Stefan Pejcic
# Created: 11.07.2023
# Last Modified: 18.08.2024
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




# import Python modules
from flask import Flask, Response, abort, render_template, request, send_file, g, jsonify, session, url_for, flash, redirect, get_flashed_messages
from flask_login import LoginManager, UserMixin, login_user, logout_user, current_user
from flask_jwt_extended import JWTManager, create_access_token, jwt_required, get_jwt_identity #needed for /api
import time
import threading
import logging
import psutil
import json
import signal
from flask_sqlalchemy import SQLAlchemy
import os
import random
import sys
import threading
import re
import urllib.parse
import urllib.request
import base64
import socket
import shutil
import mysql.connector
from mysql.connector import Error
from functools import wraps
from flask import current_app
import subprocess
from subprocess import getoutput, check_output
import importlib
from apscheduler.schedulers.background import BackgroundScheduler
from datetime import datetime, timedelta
import requests
import docker
from flask_basicauth import BasicAuth
import os
import hashlib

import pty
import select
import setproctitle
setproctitle.setproctitle("openadmin")
import logging
from logging.handlers import RotatingFileHandler
from flask_minify import Minify, decorators as minify_decorators # minify



app = Flask(__name__)

# Clear the Jinja2 template cache
app.jinja_env.cache = {}

# Open configuration file
#
#  Helper function to read and parse conf file
#
def load_openpanel_config(config_file_path):
    config_data = {}
    with open(config_file_path, 'r') as file:
        for line in file:
            line = line.strip()
            if line.startswith('['):
                section_title = line.strip('[]')
            elif line and '=' in line:
                key, value = line.split('=', 1)
                if section_title:
                    if section_title not in config_data:
                        config_data[section_title] = {}
                    config_data[section_title][key] = value
    return config_data

# Load the configuration data
config_file_path = '/etc/openpanel/openpanel/conf/openpanel.config'
config_data = load_openpanel_config(config_file_path)






# BASIC-AUTH
#
# added in 0.1.6
#

basic_auth_enabled = config_data.get('PANEL', {}).get('basic_auth', 'no') == 'yes'

if basic_auth_enabled:
    basic_auth_username = config_data.get('PANEL', {}).get('basic_auth_username', 'admin')
    basic_auth_password = config_data.get('PANEL', {}).get('basic_auth_password')

    # Enable BasicAuth only when both username and password are set
    if basic_auth_username and basic_auth_password:
        app.config['BASIC_AUTH_USERNAME'] = basic_auth_username
        app.config['BASIC_AUTH_PASSWORD'] = basic_auth_password

        basic_auth = BasicAuth(app)
    else:
        # Disable BasicAuth if username or password is missing
        print("Warning: Basic authentication is enabled in the conf file but username or password is missing. BasicAuth it therefor not active.")
        class DummyBasicAuth:
            def required(self, view_func):
                return view_func
        basic_auth = DummyBasicAuth()
else:
    # BasicAuth is disabled
    class DummyBasicAuth:
        def required(self, view_func):
            return view_func
    basic_auth = DummyBasicAuth()


################################################################################
#
#  SQLite DATABASE CONNECTION
#
# other modules should import database like this:
# import database from app import db
app.config['SQLALCHEMY_DATABASE_URI'] = 'sqlite:////etc/openpanel/openadmin/users.db'
app.config['SQLALCHEMY_TRACK_MODIFICATIONS'] = False
app.config['USER_IS_ACTIVE'] = lambda user: user.is_active
db = SQLAlchemy(app)

################################################################################

# JWT for api endpoints
#
# api.py should import both app.py and Users from login:
# from modules.login import User
# from app import app 
#
app.config['JWT_SECRET_KEY'] = 'tajnokaoarea51'
app.config['SESSION_TYPE'] = 'filesystem'
jwt = JWTManager(app)

# General stuff
app.debug = False
app.secret_key = 'Da168IGFs2F'

# current panel version
version_file_path = "/usr/local/panel/version"
try:
    with open(version_file_path, 'r') as version_file:
        panel_version = version_file.read().strip()
except FileNotFoundError:
    panel_version = "Unknown"


################################################################################
#
# HELPER FUNCTIONS START HERE
#
################################################################################

# LICENSE
LICENSE_CHECK_INTERVAL = 21600  # 6 hours
license_lock = threading.Lock()
last_license_check_time = None
previous_license_validity = False
# License verification
#
# runs every time the admin area is accessed
#
def is_license_valid():
    global last_license_check_time
    global previous_license_validity

    current_time = datetime.now()

    # Acquire the lock before accessing shared resources
    with license_lock:
        current_time = datetime.now()
        # Check if the license check interval has not passed
        if last_license_check_time and (current_time - last_license_check_time).total_seconds() < LICENSE_CHECK_INTERVAL:
            return previous_license_validity


        # License check function using WHMCS server
        def check_license(license_key, local_key=''):
            whmcs_url = 'https://my.openpanel.com/'
            licensing_secret_key = 'stefanarchy'
            local_key_days = 15
            allow_check_fail_days = 5

            def md5_hash(data):
                return hashlib.md5(data.encode()).hexdigest()

            def remote_license_check(post_data):
                url = urllib.parse.urljoin(whmcs_url, 'modules/servers/licensing/verify.php')
                #print("URL used for the request:", url)  # Print the URL used for the request
                query_string = urllib.parse.urlencode(post_data).encode()
                #print("Request sent:", query_string)  # Print the request sent
                response = urllib.request.urlopen(url, query_string)
                response_content = response.read().decode()
                #print("Response received:", response_content)  # Print the response received
                return response_content

            def is_local_key_valid():
                nonlocal local_key
                if not local_key:
                    previous_license_validity = False
                    return False
                
                local_key = local_key.replace("\n", '')
                local_data = local_key[:-32]
                md5_hash_val = local_key[-32:]

                if md5_hash(local_data + licensing_secret_key) == md5_hash_val:
                    local_data = local_data[::-1]
                    md5_hash_val = local_data[:32]
                    local_data = local_data[32:]
                    local_data = base64.b64decode(local_data)
                    local_key_results = json.loads(local_data)

                    original_check_date = local_key_results['checkdate']
                    if md5_hash(original_check_date + licensing_secret_key) == md5_hash_val:
                        local_expiry = (datetime.now() - timedelta(days=local_key_days)).strftime('%Y%m%d')
                        if original_check_date > local_expiry:
                            previous_license_validity = True
                            return True
                previous_license_validity = False
                return False

            def get_public_ip():
                try:
                    response = urllib.request.urlopen("https://ip.openpanel.com/")
                    return response.read().decode().strip()
                except Exception as e:
                    print("Failed to retrieve public IP:", e)
                    return None

            check_token = str(int(time.time())) + md5_hash(str(random.randint(100000000, sys.maxsize)) + license_key)
            check_date = datetime.now().strftime('%Y%m%d')
            domain = socket.gethostname()
            user_ip = get_public_ip()

            # DEBUG
            #print("- DATE: ", check_date)
            #print("- HOSTNAME: ", domain)
            #print("- IP: ", user_ip)
            #print("- LICENSE KEY: ", license_key)


            if not user_ip:
                return {'status': 'Invalid', 'description': 'Failed to retrieve public IP'}

            if is_local_key_valid():
                return local_key_results

            post_fields = {
                'licensekey': license_key,
                'ip': user_ip
            }
            if check_token:
                post_fields['check_token'] = check_token

            try:
                response = remote_license_check(post_fields)
            except Exception as e:
                return {'status': 'Invalid', 'description': 'Remote Check Failed'}

            response_data = dict(re.findall(r'<(.*?)>([^<]+)</\1>', response))

            if 'md5hash' in response_data and response_data['md5hash'] != md5_hash(licensing_secret_key + check_token):
                return {'status': 'Invalid', 'description': 'MD5 Checksum Verification Failed'}

            if response_data['status'] == 'Active':
                response_data['checkdate'] = check_date
                data_encoded = json.dumps(response_data).encode()
                data_encoded = base64.b64encode(data_encoded)
                data_encoded = md5_hash(check_date + licensing_secret_key) + data_encoded.decode()
                data_encoded = data_encoded[::-1]
                data_encoded += md5_hash(data_encoded + licensing_secret_key)
                data_encoded = '\n'.join([data_encoded[i:i+80] for i in range(0, len(data_encoded), 80)])
                response_data['localkey'] = data_encoded

            response_data['remotecheck'] = True
            #print(response_data)
            previous_license_validity = True
            return response_data

        # check if license key exists.
        license_key = config_data.get('LICENSE', {}).get('key')
        local_key = ""  # cached value also in ram only, tmpfs

        # Validate license key using WHMCS server
        results = check_license(license_key, local_key)
        
        #DEBUG ONLYprint("License check results:", results)

        status = results.get('status')
        last_license_check_time = current_time

        if status == "Active":
            previous_license_validity = True
            return True
        elif status in ["Invalid", "Expired", "Suspended"]:
            #print(f"License key is {status}")
            previous_license_validity = False
            return False
        else:
            print("Invalid Response")
            previous_license_validity = False
            return False
        # fallback
        return False




# Login required
#
# make sure only logged in users can access all routes
#
def login_required_route(func):
    @wraps(func)
    def wrapper(*args, **kwargs):
        if not current_user.is_authenticated:
            return redirect(url_for('login', next=request.url))
        return func(*args, **kwargs)
    return wrapper


# license page

# Render license_error.html without using inject_data
@app.route('/license_error')
@login_required_route
def render_license_error():
    # Render the template directly without passing through inject_data
    return render_template('license_error.html')



def get_public_ip():
    try:
        response = urllib.request.urlopen("https://ip.openpanel.com/")
        return response.read().decode().strip()
    except Exception as e:
        print("Failed to retrieve public IP:", e)
        return "Unknown"



# Inject data before requests
#
#  Steps we need to do before every request
#
@app.context_processor
def inject_data():
    # Get the public IP address using the socket module
    public_ip = get_public_ip()

    # check if license key exists.
    license_key = config_data.get('LICENSE', {}).get('key')
    #license_key = "enterprise-7fd563bdca"  # will read from file.

    if not license_key or not license_key.startswith('enterprise'):
        license_type = "Community"
    else:
        license_type = "Enterprise"

    # get the installed openpanel version
    version_file_path = '/usr/local/panel/version'


    # Load the configuration data
    force_domain_value = config_data.get('DEFAULT', {}).get('force_domain', '')
    server_hostname = socket.gethostname() or "OpenPanel"

    # Load notifications from v0.1.5 and newer
    log_dir = "/var/log/openpanel/admin"
    log_file = os.path.join(log_dir, 'notifications.log')

    try:
        # Open the file and read the last 5 lines
        with open(log_file, 'r') as f:
            lines = f.readlines()
            last_5_lines = lines[-5:][::-1]  # Take the last 5 lines and reverse the order

        # Extract data until "MESSAGE" for each line
        last_5_unread_notifications = []
        for notification in last_5_lines:
            message_index = notification.find("MESSAGE:")
            if message_index != -1:
                data_until_message = notification[:message_index].strip()
                last_5_unread_notifications.append(data_until_message)

        # Count unread notifications
        total_unread_notifications = sum('UNREAD' in notification for notification in last_5_lines)

    except Exception as e:
        last_5_unread_notifications = []
        total_unread_notifications = 0

    if os.path.exists(version_file_path):
        with open(version_file_path, 'r') as version_file:
            panel_version = version_file.read().strip()
    else:
        panel_version = "unrecognized"

    return dict(panel_version=panel_version, public_ip=public_ip, server_hostname=server_hostname,
                force_domain_value=force_domain_value, last_5_notifications=last_5_unread_notifications,
                unread_notifications=total_unread_notifications, license_type=license_type)



# Before requests
#
#  Make sure that the version is accessible on all templates
#
@app.before_request
@basic_auth.required
def set_panel_version():

    # check if license key exists.
    license_key = config_data.get('LICENSE', {}).get('key')
    #license_key = "enterprise-7fd563bdca"  # will read from file.

    if not license_key or not license_key.startswith('enterprise'):
        license_type = "Community"
    else:
        license_type = "Enterprise"

        exempt_routes = ['license_error', 'login']  # Add any exempt routes here
        if request.endpoint in exempt_routes:
            return  

        # Check if the endpoint is license_error
        if request.endpoint == 'license_error' or request.endpoint == 'login':
            return {}
        else:
            if not is_license_valid():
                print("License key: ", license_key, " is invalid.")
            else:
                print("License is valid. Continuing with request processing.")



    g.panel_version = panel_version

# After requests
#
#  Add CORS headers after each request
#

#@app.after_request
#def after_request(response):
#    response.headers['Access-Control-Allow-Origin'] = 'https://demo.openpanel.org'
#    response.headers['Access-Control-Allow-Methods'] = 'GET, POST, PUT, DELETE, OPTIONS'
#    response.headers['Access-Control-Allow-Headers'] = 'Content-Type, Authorization'
#    return response

#
# DISABLED FOR PRODUCTION, ONLY LEAVE FOR DEMO.OPENPANEL.CO
#
###########################################################################
#
#  MYSQL DATABASE CONENCTION
#

# Close db connection
#
#  ZA BAZU KAKO BI IZBEGLI TIMEOUT, KONKECIJA SE USPOSTAVLJA I GASI PRI SVAKOM ZAHTEVU
#
conn = None

# Read db login information
#
#  Read stored myssql db logins and use them for sql queries
#
CONFIG_FILE_PATH = '/etc/openpanel/mysql/db.cnf' #changed in 0.2.0 from /usr/local/admin/db.cnf

# connect
def connect_to_database():
    try:
        # Use the my.cnf file for connection configuration
        conn = mysql.connector.connect(option_files=CONFIG_FILE_PATH)
        return conn
    except Error as err:
        # Handle the error and return an error response
        error_message = f"Database connection error: {err}"
        return jsonify({"error": error_message}), 500

# disconnect
def close_database_connection(conn):
    if conn:
        conn.close()

###########################################################################

# connect to mysql only when needed
#
# Make sure we do NOT connect to mysql on static resources and login page
#
# Also make sure user accepted the terms! # added in 0.2.0
#
@app.before_request
def before_request():
    if request.path.startswith('/api') or request.path == '/login' or request.path == '/send_email' or request.path.startswith('/static/') or request.path == '/terms':
        return
    terms_path = '/etc/openpanel/openadmin/config/terms'
    if os.path.exists(terms_path):
        return redirect(url_for('terms'))
    connect_to_database()



@app.route('/terms', methods=['GET', 'POST'])
@login_required_route
def terms():
    terms_path = '/etc/openpanel/openadmin/config/terms'
    if request.method == 'POST':
        # Rename the file to include the timestamp
        if os.path.exists(terms_path):
            timestamp = time.strftime("%Y%m%d%H%M%S")
            new_path = f"{terms_path}_accepted_on_{timestamp}"
            os.rename(terms_path, new_path)
            return redirect(url_for('dashboard'))
    return render_template('terms.html')


# disconnect from mysql when needed
#
#  disconnect after every page finishes loading
#
@app.teardown_request
def teardown_request(exception=None):
    close_database_connection(conn)

# get username from user id
#
#  helper mysql function
#
def query_username_by_id(user_id):
    cursor = conn.cursor()
    query = "SELECT username FROM users WHERE id = %s"
    cursor.execute(query, (user_id,))
    result = cursor.fetchone()
    cursor.close()
    return result[0] if result else None

# not sure this is used anymore!
#
@app.route('/get_container_name/<container_id>')
def get_container_name(container_id):
    try:
        # Get the container object
        container = docker_client.containers.get(container_id)
        
        # Get the container name
        container_name = container.name

        return jsonify({"username": container_name})
    
    except docker.errors.NotFound:
        return jsonify({"error": "User not found"}), 404

################################################################################
#
# HELPER FUNCTIONS END HERE
#
################################################################################







################################################################################
#
# Core modules
#
#  since 0.1.5< we use separate python files and need to import them
#
from modules import license, notifications, dashboard, users, plans, login, logout, autologin, profile

from modules.settings import general, openadmin, openpanel, backups, settings

from modules.security import firewall, waf

from modules.services import status, docker, nginx, logs, resources

from modules.domains import domains

# added in v.0.2.7
license_key = config_data.get('LICENSE', {}).get('key')
if license_key and license_key.startswith('enterprise'):
    from modules import emails


from modules.general import errors, search

from modules.general.mailer import smtp_mailer

# added in v.0.2.2
from modules.server import ssh, root_password, cronjobs, timezone, drop_ram

# added in v.0.2.4
from modules import importer


# here we will load all premium modules if license key exists in conf file
# and then before every request we check if license is valid, if not block access
#
license_key = config_data.get('LICENSE', {}).get('key')
if license_key:

    from modules.api import api #todo: premium only message

    # REST API AND WHMCS
    api_status_current_value = config_data.get('PANEL', {}).get('api', 'off')
    # import /api only when admin enabled
    if api_status_current_value.lower() == 'on':
        from modules.api import endpoints

    # SETTINGS > TEMPLATES
    #from modules.settings import templates

    # FTP
    ftp_module_path = '/usr/local/admin/modules/ftp.py' # ftp module is added in v0.1.9
    if os.path.exists(ftp_module_path):
        from modules import ftp

    
    '''
    # EMAILS
    email_module_path = '/usr/local/admin/modules/emails.py' # email module is added in v0.2.0
    if os.path.exists(email_module_path):
        from modules import email
    '''
    # CPANEL
    cpanel_module_path = '/usr/local/admin/modules/cpanel_import.py' # cpanel-import module is added in v0.2.1
    if os.path.exists(cpanel_module_path):
        from modules import cpanel_import

# mailer
smtp_mailer(app)




################################################################################

# DEVELOPMENT MODE
#
# in production all responses *(html, js, css, json) are minified
# for development this should be disbaled by setting:
#
# opencli config update dev_mode on && service admin reload
#
dev_mode = config_data.get('PANEL', {}).get('dev_mode', 'off') # off for versions <0.1.6

if dev_mode.lower() == 'off':
    minify = Minify(app=app, html=True, js=True, cssless=True, bypass=['api.*', 'domains/export-dns-zone/<domain>'])
else:
    minify = Minify(app=app, passive=True)


################################################################################

# CUSTOM ADMIN TEMPLATE
#
# added in 0.1.6
#
admin_template_folder = config_data.get('PANEL', {}).get('admin_template', 'templates')

if admin_template_folder:
    app.template_folder = admin_template_folder
else:
    app.template_folder = 'templates'




# Main function
#
#  yay!
#
if __name__ == '__main__':
    app.config['SECRET_KEY'] = 'strogo_poslovno_secret'
    app.run(debug=True, host='0.0.0.0', port=2087)
