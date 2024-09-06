################################################################################
# Author: Stefan Pejcic
# Created: 11.07.2023
# Last Modified: 04.04.2024
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
import json
from flask import Flask, Response, render_template, request, g, jsonify, session, redirect, url_for, flash
import subprocess
import datetime
import psutil
import docker
import platform
import time
from datetime import datetime, timedelta

import distro

# import our modules
from app import app, is_license_valid, login_required_route, load_openpanel_config, connect_to_database




@app.route('/json/show_usage_stats')
@login_required_route
def show_server_usage_stats():
    try:
        with open('/etc/openpanel/openadmin/usage_stats.json', 'r') as file:
            content = file.read()
        non_empty_lines = '\n'.join(filter(lambda line: line.strip(), content.splitlines()))

        return jsonify({"usage_stats": non_empty_lines})
    except FileNotFoundError:
        return jsonify({"error": "File not found"}), 404





# used on dashboard
@app.route('/json/user_activity_status')
@login_required_route
def user_activity_status():
    base_path = '/etc/openpanel/openpanel/core/users'
    users_status = {}

    for user in os.listdir(base_path):
        user_path = os.path.join(base_path, user, 'activity.log')
        try:
            with open(user_path, 'r') as log_file:
                last_line = log_file.readlines()[-1]
                timestamp_str = last_line.split()[0] + ' ' + last_line.split()[1]
                timestamp = datetime.strptime(timestamp_str, '%Y-%m-%d %H:%M:%S')
                if datetime.now() - timestamp <= timedelta(hours=1):
                    users_status[user] = 'active'
                else:
                    users_status[user] = 'inactive'
        except (FileNotFoundError, IndexError):
            users_status[user] = 'inactive'  # Assuming inactive if log is missing or empty

    return jsonify(users_status)


# used on dashboard
@app.route('/json/system')
@login_required_route
def system_info():
    # Basic system info
    info = {
        'hostname': platform.node(),
        'os': distro.name(pretty=True), # + " " + platform.release(),
        'time': time.strftime('%Y-%m-%d %H:%M:%S', time.localtime()),
        'kernel': platform.release(),
        'cpu': platform.processor(),
    }

    # OpenPanel version
    try:
        with open('/usr/local/panel/version', 'r') as version_file:
            open_panel_version = version_file.read().strip()
            info['openpanel_version'] = open_panel_version
    except Exception as e:
        info['openpanel_version'] = 'Unavailable'

    # Get exact CPU model
    try:
        lscpu_output = subprocess.check_output(['lscpu']).decode('utf-8')
        for line in lscpu_output.split('\n'):
            if 'Model name:' in line:
                cpu_model = line.split(':')[1].strip()
                info['cpu'] = cpu_model + "(" + platform.processor() + ")"
                break
    except Exception as e:
        info['cpu'] = 'Unavailable'

    # Uptime
    try:
        with open('/proc/uptime', 'r') as f:
            uptime_seconds = float(f.readline().split()[0])
            info['uptime'] = str(int(uptime_seconds))
    except Exception as e:
        info['uptime'] = 'Unavailable'
    
    # Running Processes
    try:
        ps_output = subprocess.check_output(['ps', '-e']).decode('utf-8')
        running_processes = ps_output.count('\n')
        info['running_processes'] = running_processes
    except Exception as e:
        info['running_processes'] = 'Unavailable'
    
    # Package updates
    try:
        updates_output = subprocess.check_output(['apt', 'list', '--upgradable'], stderr=subprocess.DEVNULL).decode('utf-8')
        updates_count = updates_output.count('\n') - 1
        info['package_updates'] = updates_count
    except Exception as e:
        info['package_updates'] = 'Unavailable'
    
    return jsonify(info)


# used by: users
def get_all_users():
    conn = connect_to_database()
    cursor = conn.cursor()
    cursor.execute("SELECT users.*, plans.name FROM users INNER JOIN plans ON users.plan_id = plans.id;")
    users = cursor.fetchall()
    cursor.close()
    conn.close()
    return users

# used by: dashboard
def get_user_and_plan_count():
    conn = connect_to_database()
    cursor = conn.cursor()
    cursor.execute("SELECT (SELECT COUNT(*) FROM users) AS user_count, (SELECT COUNT(*) FROM plans) AS plan_count")
    counts = cursor.fetchone()
    cursor.close()
    conn.close()
    return counts


# used by: plans
def get_plan_by_id(plan_id):
    conn = connect_to_database()
    cursor = conn.cursor(dictionary=True)
    query = "SELECT * FROM plans WHERE id = %s"
    cursor.execute(query, (plan_id,))
    plan = cursor.fetchone()
    cursor.close()
    conn.close()
    
    return plan


# used by: plans
def get_all_plans():
    conn = connect_to_database()
    cursor = conn.cursor(dictionary=True)
    cursor.execute("SELECT * FROM plans")
    plans = cursor.fetchall()
    cursor.close()
    conn.close()
    return plans



# used by: domains
def get_all_domains():
    conn = connect_to_database()
    cursor = conn.cursor(dictionary=True)
    query = """
    SELECT 
        d.domain_id, 
        d.domain_name, 
        d.domain_url, 
        u.username
    FROM 
        domains d
    JOIN 
        users u 
    ON 
        d.user_id = u.id
    """
    cursor.execute(query)
    domains = cursor.fetchall()
    cursor.close()
    conn.close()
    return domains


# enterprise only!
def get_all_emails():
    emails_and_quotas = []
    file_path = '/usr/local/mail/openmail/docker-data/dms/config/postfix-accounts.cf'
    
    try:
        # Attempt to get emails and quotas using the opencli command
        result = subprocess.run(['opencli', 'email-setup', 'email', 'list'], 
                                stdout=subprocess.PIPE, 
                                stderr=subprocess.PIPE, 
                                text=True, 
                                check=True)
        
        # Parse the output
        for line in result.stdout.splitlines():
            if line.startswith('*'):
                parts = line.split()
                email = parts[1]
                quota = ' '.join(parts[2:])
                emails_and_quotas.append((email, quota))
    
    except subprocess.CalledProcessError:
        # If opencli fails, fallback to reading the file directly (emails only)
        print("opencli command failed, falling back to reading the file directly.")
        try:
            with open(file_path, 'r') as file:
                for line in file:
                    if '|' in line:
                        email = line.split('|')[0].strip()
                        if email:
                            emails_and_quotas.append((email, None))  # No quota info available
        except FileNotFoundError:
            print(f"File not found: {file_path}")
        except Exception as e:
            print(f"An error occurred: {e}")
    
    return emails_and_quotas





# used by: users, plans
def get_userdata_by_username(username):
    conn = connect_to_database()
    cursor = conn.cursor()
    query = """
    SELECT u.username, u.id, u.email, u.services, d.domain_name, u.twofa_enabled, u.registered_date, u.plan_id
    FROM users u
    LEFT JOIN domains d ON u.id = d.user_id
    WHERE u.username = %s
    """
    cursor.execute(query, (username,))
    user_data = cursor.fetchall()
    
    if user_data:
        user = {
            "username": user_data[0][0],
            "id": user_data[0][1],
            "email": user_data[0][2],
            "services": user_data[0][3],
            "user_domains": {row[4]: row[5] for row in user_data} if any(row[4] for row in user_data) else None,
            "registered_date": user_data[0][6],
            "plan_id": user_data[0][7]
        }
    else:
        user = None
    
    cursor.close()
    conn.close()
    
    return user

# used by: users, plans
def get_hosting_plan_name_by_id(hosting_plan):
    conn = connect_to_database()
    cursor = conn.cursor()
    select_plan_name_query = """
    SELECT name FROM plans
    WHERE id = %s;
    """
    cursor.execute(select_plan_name_query, (hosting_plan,))
    plan_name = cursor.fetchone()
    return plan_name[0] if plan_name else None
    cursor.close()
    conn.close()

# used by: users
def get_user_websites(user_id):
    conn = connect_to_database()
    cursor = conn.cursor()
    select_data_query = """SELECT site_name, version, created_date, type FROM sites WHERE domain_id IN (SELECT domain_id FROM domains WHERE user_id = %s);"""
    cursor.execute(select_data_query, (user_id,))
    data = cursor.fetchall()
    cursor.close()
    conn.close()
    return data

# used by: users
def is_username_unique(username):
    cursor = conn.cursor()

    query = "SELECT id FROM users WHERE username = %s"
    cursor.execute(query, (username,))

    result = cursor.fetchone()
    cursor.close()

    return False if result else True









# used by docker settings page
@app.route('/json/docker-info', methods=['GET'])
@login_required_route
def docker_info():
    try:
        client = docker.from_env()
        docker_info = client.info()

        if request.args.get('format') == 'json':
            return jsonify(docker_info)
        else:
            formatted_info = "\n".join([f"{key}: {value}" for key, value in docker_info.items()])
            return formatted_info
    except Exception as e:
        return jsonify({'error': str(e)}), 500







import hashlib
# used by: users
def gravatar_url(email, size=150):
    if email is None:
        return None  # Handle the case where email is None
    gravatar_base_url = "https://www.gravatar.com/avatar/"
    email_hash = hashlib.md5(email.lower().encode()).hexdigest()
    return f"{gravatar_base_url}{email_hash}?s={size}&d=identicon"
