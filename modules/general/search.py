# core/search.py
# *************************************************************************
# *                                                                       *
# * OpenAdmin                                                             *
# * Copyright (c) OpenPanel. All Rights Reserved.                         *
# * Version: 1.3.2                                                        *
# * Build Date: 2025-05-27 19:36:31                                       *
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
from flask_babel import Babel, _ # https://python-babel.github.io/flask-babel/
import os
import json
import socket
from flask import Flask, Response, abort, render_template, request, send_file, g, jsonify, session, url_for, flash, redirect, get_flashed_messages
import subprocess
import datetime
import psutil
from app import app, cache, is_license_valid, admin_required_route, load_openpanel_config, connect_to_database
import docker

from modules.helpers import get_all_users, get_user_and_plan_count, get_plan_by_id, get_all_plans, get_userdata_by_username, get_hosting_plan_name_by_id




filtered_json_file_path = "/usr/local/admin/core/search/filtered.json"
fallback_json_file_path = "/usr/local/admin/core/search/filter.json"

# Check if the filtered.json file exists, if not, use the fallback filter.json file
if os.path.exists(filtered_json_file_path):
    json_file_path = filtered_json_file_path
else:
    json_file_path = fallback_json_file_path
try:
    with open(json_file_path, 'r') as file:
        all_routes = json.load(file)
except FileNotFoundError:
    print(f"Error: File '{json_file_path}' not found.")

@app.route('/core/search_filter')
@admin_required_route
def search_filter():
    limit_results = 100
    if os.path.exists(json_file_path):
        try:
            with open(json_file_path, 'r') as json_file:
                data = json.load(json_file)
                return jsonify(data[:limit_results])
        except Exception as e:
            return jsonify({'error': _('Error loading JSON data')}), 500
    else:
        return jsonify([]), 404



@app.route('/domains/<path:domain_name>')
@admin_required_route
def get_domain_owner(domain_name):
    domain_name = domain_name.split('/', 1)[0]
    command = ["opencli", "domains-whoowns", domain_name]
    try:
        output = subprocess.check_output(command, text=True)
        # Check if the domain exists
        if "not found in the database" in output:
            if 'output' in request.args and request.args['output'] == 'json':
                return jsonify({'error': 'Domain not found'}), 404
            else:
                return render_template('system/custom.html', message='Domain not found')
        else:
            username = output.split(':')[-1].strip()
            if 'output' in request.args and request.args['output'] == 'json':
                return jsonify({'domain_name': domain_name, 'username': username})
            else:
                return redirect(f'/users/{username}#nav-user-data')
    except subprocess.CalledProcessError as e:
        # Log the error
        app.logger.error(f"Error executing command: {e}")
        if 'output' in request.args and request.args['output'] == 'json':
            return jsonify({'error': 'Internal Server Error'}), 500
        else:
            return render_template('system/custom.html', message='Internal Server Error')

@cache.memoize(timeout=300)
def get_all_websites():
    conn = connect_to_database()
    cursor = conn.cursor()

    select_data_query = """
    SELECT site_name
    FROM sites
    """
    cursor.execute(select_data_query)
    data = cursor.fetchall()
    cursor.close()
    conn.close()
    return data


@app.route('/core/search_websites', defaults={'site_name': None})
@app.route('/core/search_websites/<site_name>')
@admin_required_route
def search_websites(site_name):
    if site_name:
        # search sites
        conn = connect_to_database()
        cursor = conn.cursor()
        select_data_query = """
        SELECT site_name
        FROM sites
        WHERE site_name LIKE %s
        LIMIT 5
        """
        cursor.execute(select_data_query, (f'%{site_name}%',))
        data = cursor.fetchall()
        cursor.close()
        conn.close()
    else:
        # get all sites
        data = get_all_websites()

    return jsonify(data[:10])



@cache.memoize(timeout=300)
def get_all_users():
    conn = connect_to_database()
    cursor = conn.cursor()

    select_data_query = """
    SELECT username
    FROM users
    """
    cursor.execute(select_data_query)
    data = cursor.fetchall()
    cursor.close()
    conn.close()
    return data

@app.route('/core/search_users', defaults={'username': None})
@app.route('/core/search_users/<username>')
@admin_required_route
def search_users(username):
    if username:
        # search sites
        conn = connect_to_database()
        cursor = conn.cursor()
        select_data_query = """
        SELECT username
        FROM users
        WHERE username LIKE %s
        LIMIT 5
        """
        cursor.execute(select_data_query, (f'%{username}%',))
        data = cursor.fetchall()
        cursor.close()
        conn.close()
    else:
        # get all sites
        data = get_all_users()

    return jsonify(data[:10])


