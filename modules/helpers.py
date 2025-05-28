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
from flask import Flask, Response, request, g, jsonify, session
import subprocess
import datetime
import psutil
import docker
import platform
import re
import time
from datetime import datetime, timedelta
from flask_login import current_user # needed for reseller checks!

import distro

# import our modules
from app import app, cache, is_license_valid, login_required_route, admin_required_route, load_openpanel_config, connect_to_database, get_openpanel_version


# will be implemented on all routes that show per user data, to chekc if current reseller is the user owner!
def check_if_owner_for_user(username):
    try:
        conn = connect_to_database()
        cursor = conn.cursor()

        # Get current user's role
        user_role = getattr(current_user, 'role', 'reseller')
        
        if user_role == 'reseller':
            # For resellers, check if they are the owner
            owner = getattr(current_user, 'username')

            # Query to check if the username is an 'owner'
            query = """
            SELECT 1 FROM users
            WHERE username = %s AND owner = %s
            LIMIT 1
            """
            
            # Execute the query
            cursor.execute(query, (username, owner))
            result = cursor.fetchone()

            if result:
                return True
            else:
                return False

        elif user_role != 'reseller':
            # If the role is not 'reseller', we allow access if the owner is NULL
            query = """
            SELECT owner FROM users
            WHERE username = %s
            LIMIT 1
            """
            
            # Execute the query
            cursor.execute(query, (username,))
            result = cursor.fetchone()

            # Check if owner is NULL or not found, we can show the data
            if result:
                return True
            else:
                return True

        # Close connections
        cursor.close()
        conn.close()

        return False  # Default to False if the role isn't recognized

    except AttributeError as e:
        print(f"Error in check_if_user_owner: {e}")
        return False
    except Exception as e:
        print(f"Unexpected error in check_if_user_owner: {e}")
        return False



# used by dashboard
# matches unix that have no hostfs (default and ssh)
def get_docker_contexts():
    try:
        result = subprocess.run(['docker', 'context', 'ls', '--format', '{{.Name}} {{.DockerEndpoint}}'], stdout=subprocess.PIPE, stderr=subprocess.PIPE, text=True)
        if result.stderr:
            raise Exception(result.stderr.strip())
        
        contexts = result.stdout.strip().split('\n')
        filtered_contexts = [ctx for ctx in contexts if 'hostfs' not in ctx]
        
        return len(filtered_contexts)
    except Exception as e:
        print(f"Error getting Docker contexts: {e}")
        return 0





# used on dashboard
@app.route('/json/user_activity_status')
@admin_required_route
@cache.cached(timeout=600)
def user_activity_status():
    base_path = '/etc/openpanel/openpanel/core/users'
    users_status = {}

    for user in os.listdir(base_path):
        if user == '.gitkeep':
            continue
        if user == 'repquota':
            continue
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
@admin_required_route
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
    info['openpanel_version'] = get_openpanel_version()

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
    try:
        conn = connect_to_database()
        if conn is None:
            return -1
        cursor = conn.cursor()
        
        # Default query for admin and general users
        query = """
            SELECT users.*, plans.name, plans.disk_limit, plans.inodes_limit, plans.cpu, plans.ram
            FROM users 
            INNER JOIN plans ON users.plan_id = plans.id;
        """

        # Query modification for resellers
        if getattr(current_user, 'role', 'reseller') == 'reseller':
            owner = getattr(current_user, 'username')
            query = """
                SELECT users.*, plans.name, plans.disk_limit, plans.inodes_limit, plans.cpu, plans.ram
                FROM users
                INNER JOIN plans ON users.plan_id = plans.id
                WHERE users.owner = %s;
            """
        
        # Execute the final query
        cursor.execute(query, (owner,) if 'WHERE' in query else ())
        users = cursor.fetchall()
        
        cursor.close()
        conn.close()
        return users
    except AttributeError as e:
        print(f"Error in get_all_users: {e}")
        return []
    except Exception as e:
        print(f"Unexpected error in get_all_users: {e}")
        return []

# used by: dashboard in <0.3.8
def get_user_and_plan_count():
    conn = connect_to_database()
    cursor = conn.cursor()
    cursor.execute("SELECT (SELECT COUNT(*) FROM users) AS user_count, (SELECT COUNT(*) FROM plans) AS plan_count")
    counts = cursor.fetchone()
    cursor.close()
    conn.close()
    return counts

# used by dashboard
@cache.cached(timeout=600)
def get_containers_count_from_all_contexts():
    result = subprocess.run(
        ["docker", "context", "ls", "--format", "{{.Name}}"],
        capture_output=True, text=True
    )

    if result.returncode != 0:
        return -1

    contexts = result.stdout.strip().splitlines()
    total_containers = 0

    for context in contexts:
        cmd = ["docker", "--context", context, "container", "ls", "-a", "-q"]
        containers = subprocess.run(cmd, capture_output=True, text=True)
        if containers.returncode != 0:
            continue

        container_list = containers.stdout.strip().splitlines()
        total_containers += len(container_list)

    return total_containers

# used by: dashboard and daily reports
def get_counts_from_db():
    conn = connect_to_database()
    cursor = conn.cursor()
    cursor.execute("""
        SELECT 
            (SELECT COUNT(*) FROM users) AS user_count, 
            (SELECT COUNT(*) FROM plans) AS plan_count,
            (SELECT COUNT(*) FROM sites) AS site_count,
            (SELECT COUNT(*) FROM domains) AS domain_count
    """)
    counts = cursor.fetchone()
    cursor.close()
    conn.close()
    return counts


# used by: plans
def get_plan_by_id(plan_id):

    # todo: check reseller!
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
    try:
        conn = connect_to_database()
        if conn is None:
            return -1
        cursor = conn.cursor(dictionary=True)
        query = "SELECT * FROM plans"
        params = []

        # Check if the current user is a reseller
        if getattr(current_user, 'role', 'reseller') == 'reseller':
            owner = getattr(current_user, 'username', None)
            if owner:
                reseller_file = f"/etc/openpanel/openadmin/resellers/{owner}.json"
                if os.path.exists(reseller_file):
                    with open(reseller_file, 'r') as f:
                        reseller_data = json.load(f)
                        allowed_plans = reseller_data.get("allowed_plans", [])
                        
                        if allowed_plans:  # Only filter if allowed_plans is not empty
                            placeholders = ','.join(['%s'] * len(allowed_plans))
                            query = f"SELECT * FROM plans WHERE id IN ({placeholders})"
                            params = allowed_plans
        
        cursor.execute(query, params)
        plans = cursor.fetchall()
        
        cursor.close()
        conn.close()
        return plans
    except AttributeError as e:
        print(f"Error in get_all_plans: {e}")
        return []  # Return an empty list as a fallback
    except Exception as e:
        print(f"Unexpected error in get_all_plans: {e}")
        return []




# used by: domains
def get_all_domains():
    conn = connect_to_database()
    if conn is None:
        return -1
    cursor = conn.cursor(dictionary=True)
    query = """
    SELECT 
        d.domain_id, 
        d.docroot, 
        d.domain_url, 
        d.php_version,
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
#@cache.memoize(timeout=600)
def parse_ftp_accounts(raw_data, source='environment'):
    ftp_accounts = []
    if raw_data.startswith('USERS="'):
        raw_data = raw_data[len('USERS="'):]
    if raw_data.endswith('"'):
        raw_data = raw_data[:-1]

    accounts = [a for a in raw_data.strip().split(' ') if a]
    for account in accounts:
        fields = account.split('|')
        if len(fields) == 4:
            user, password, real_path, guid = fields
            op_user = user.rpartition('.')[0] 
            path = re.sub(r'.*_data/', '/var/www/html/', real_path)
            ftp_accounts.append({
                'user': user,
                #'password': password,
                'owner': op_user,
                'path': path,
                'real_path': real_path,
                'guid': guid
            })
        else:
            print(f"Skipping invalid account format from {source}: {account}")
    return ftp_accounts

def get_all_ftp_accounts():
    env_var = 'USERS'
    users_env = os.getenv(env_var)

    if users_env:
        return parse_ftp_accounts(users_env, 'environment variable')
    else:
        file_path = '/etc/openpanel/ftp/all.users'
        try:
            with open(file_path, 'r') as file:
                file_content = file.read().strip()
                return parse_ftp_accounts(file_content, f'file: {file_path}')
        except FileNotFoundError:
            print(f"Error: The file at {file_path} was not found.")
        except Exception as e:
            print(f"An error occurred while reading the file: {e}")
    
    return []


# enterprise only!
@cache.memoize(timeout=600)
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
@cache.memoize(timeout=30)
def get_userdata_by_username(username):
    try:
        conn = connect_to_database()
        if conn is None:
            return -1
        cursor = conn.cursor()
        query = """
        SELECT u.username, u.id, u.email, u.owner, u.twofa_enabled, u.registered_date, u.plan_id
        FROM users u
        WHERE u.username = %s OR u.username LIKE %s
        """
        suspended_pattern = f"SUSPENDED_%_{username}"
        cursor.execute(query, (username, suspended_pattern))
        user_data = cursor.fetchall()
        
        if user_data:
            user = {
                "username": user_data[0][0],
                "id": user_data[0][1],
                "email": user_data[0][2],
                "owner": user_data[0][3] if user_data[0][3] is not None else None,
                "twofa_enabled": user_data[0][4],
                "registered_date": user_data[0][5],
                "plan_id": user_data[0][6]
            }
        else:
            user = None
        
        cursor.close()
        conn.close()
        
        return user
    except AttributeError as e:
        print(f"Error in get_userdata_by_username: {e}")
        return None
    except Exception as e:
        print(f"Unexpected error in get_userdata_by_username: {e}")
        return None


def read_caddy_file_for_domain(domain_url):
    conf_file_path = f"/etc/openpanel/caddy/domains/{domain_url}.conf"

    # Defaults
    ssl = "none"
    status = "suspended"
    coraza = "none"

    # Check if the configuration file exists
    if os.path.exists(conf_file_path):
        with open(conf_file_path, 'r') as conf_file:
            conf_content = conf_file.read()

            # Check if the file contains specific strings to determine ssl, status, and coraza
            if "on_demand" in conf_content:
                ssl = "automatic"
            elif "fullchain.pem" in conf_content:
                ssl = "custom"
            
            if "reverse_proxy" in conf_content:
                status = "active"
            elif "file_server" in conf_content:
                status = "suspended"
            
            if "SecRuleEngine On" in conf_content:
                coraza = "on"
            elif "SecRuleEngine Off" in conf_content:
                coraza = "off"

    return ssl, status, coraza


@app.route('/domains/<domain>', methods=['GET'])
@admin_required_route
def get_domain_status(domain):
    ssl, status, coraza = read_caddy_file_for_domain(domain)
    data = {
        "domain": domain,
        "ssl": ssl,
        "status": status,
        "coraza": coraza
    }
    return jsonify(data), 200





# used by users
#
#  helper mysql function added in 0.3.0 for clustering
#
@cache.memoize(timeout=300)
def query_context_by_username(username):

    conn = connect_to_database()
    cursor = conn.cursor()
    query = """
    SELECT server FROM users
    WHERE username = %s;
    """
    cursor.execute(query, (username,))
    result = cursor.fetchone()
    return result[0] if result else None
    cursor.close()
    conn.close()


# used by: users, plans
@cache.memoize(timeout=100)
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
@cache.memoize(timeout=300)
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






@app.route('/json/php/default_version/<username>', methods=['GET','POST'])
@login_required_route
def manage_php_default_version(username):
    if not check_if_owner_for_user(username):
        abort(403)
    try:
        if request.method == 'GET':
            command = ['opencli', 'php-default', username]
            result = subprocess.run(command, capture_output=True, text=True, check=False)
            output = result.stdout.strip()
            if output.startswith(f"Default PHP version for user '{username}' is: "):
                version = output.split(": ")[1]
                return jsonify({'default_version': version})

            return jsonify({'error': 'Unexpected output format'}), 400
        
        elif request.method == 'POST':
            data = request.get_json()
            version = data.get('version')
            if not version:
                return jsonify({'error': 'Version must be provided'}), 400
            command = ['opencli', 'php-default', username, '--update', version]
            result = subprocess.run(command, capture_output=True, text=True, check=True)
            return jsonify({'message': f'Default PHP version for user \'{username}\' updated to: {version}'}), 200

    except subprocess.CalledProcessError as e:
        return jsonify({'error': 'Failed to retrieve or update default PHP version', 'details': str(e)}), 500




# get autostart services
def get_service_statuses(username):
    command = ['docker', 'exec', username, 'cat', '/etc/entrypoint.sh']
    
    try:
        output = subprocess.check_output(command, shell=True, text=True)
    except subprocess.CalledProcessError:
        return None
    
    service_status_pattern = re.compile(r'^(?P<service_name>([A-Z_]+|PHP\d{2}FPM))="(?P<status>on|off)"$')
    service_statuses = {}

    for line in output.splitlines():
        match = service_status_pattern.match(line.strip())
        if match:
            service_name = match.group('service_name')
            status = match.group('status')
            service_statuses[service_name] = status

    return service_statuses



@app.route('/json/services/<username>', methods=['GET'])
@login_required_route
def services(username):
    if not check_if_owner_for_user(username):
        abort(403)
    statuses = get_service_statuses(username)
    if statuses is None:
        abort(404, description="User container not found or inaccessible.")
    return jsonify(statuses)




import hashlib
# used by: users
@cache.memoize(timeout=300)
def gravatar_url(email, size=150):
    if email is None:
        return None  # Handle the case where email is None
    gravatar_base_url = "https://www.gravatar.com/avatar/"
    email_hash = hashlib.md5(email.lower().encode()).hexdigest()
    return f"{gravatar_base_url}{email_hash}?s={size}&d=identicon"
