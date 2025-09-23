################################################################################
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
# Author: Stefan Pejcic
# Created: 24.06.2024
# Last Modified: 26.06.2024
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
import docker
from flask import Flask, Response, abort, render_template, request, send_file, g, jsonify, session, url_for, flash, redirect, get_flashed_messages
import subprocess
import datetime
import psutil

# needed for php api calls
from functools import lru_cache
import requests

# import our modules
from app import app, cache, admin_required_route, get_openpanel_domain
from modules.helpers import get_all_domains, query_context_by_username, check_if_owner_for_user

@lru_cache(maxsize=1)
def php_versions_eol():
    api_url = "https://php.watch/api/v1/versions"
    print(f"DOMAINS - Fetching PHP version information from: {api_url}")
    try:
        response = requests.get(api_url, timeout=3)
        if response.status_code == 200:
            api_data = response.json().get('data', {})
            return {
                v['name']: {
                    'statusLabel': v['statusLabel'],
                    'isEOLVersion': v['isEOLVersion'],
                    'isSecureVersion': v['isSecureVersion'],
                    'isLatestVersion': v['isLatestVersion'],
                    'isFutureVersion': v['isFutureVersion'],
                    'isNextVersion': v['isNextVersion']
                } for v in api_data.values()
            }
        else:
            print(f"DOMAINS - Failed to fetch PHP version data: HTTP {response.status_code}")
    except requests.exceptions.Timeout:
        print("DOMAINS - The request timed out after 3 seconds.")
    except Exception as e:
        print(f"DOMAINS - An error occurred: {e}")
    return {}

# view domains
@app.route('/domains', methods=['GET'])
@app.route('/domains/', methods=['GET'])
@admin_required_route
def domains():
    current_route = request.path

    php_versions_data = php_versions_eol()
    mysql_is_down = False
    try:
        domains = get_all_domains()
        if domains == -1:
            mysql_is_down = True

        # json
        output_param = request.args.get('output')
        if output_param == 'json':
            return jsonify({'domains': domains})
        else:
            return render_template('domains/domains.html', title='Domains', php_versions_data=php_versions_data, domains=domains, app=app, current_route=current_route, mysql_is_down=mysql_is_down)

    except Exception as e:
        print(f"DOMAINS - An error occurred: {e}")

        domains = []
        new_plan_template_content = {}

        output_param = request.args.get('output')
        if output_param == 'json':
            return jsonify({'domains': domains})
        else:
            return render_template('domains/domains.html', title='Domains', php_versions_data=php_versions_data, domains=domains, app=app, current_route=current_route, mysql_is_down=mysql_is_down)
 




# edit dns domains
@app.route('/domains/dns', methods=['GET'])
@app.route('/domains/dns/<domain_name>', methods=['GET', 'POST'])
@admin_required_route
def edit_domains_dns_zones(domain_name=None):
    current_route = request.path
    if domain_name:
        domains = []
        print(f"DOMAINS - Edit DNS zone for domain: {domain_name}")

        bind_file_path = f'/etc/bind/zones/{domain_name}.zone'

        if request.method == 'POST':
            bind_content = request.form.get('bind_content') #request.json['new_content']
            try:
                print(f"DOMAINS - Updating: {bind_file_path}")
                with open(bind_file_path, 'w') as file:
                    file.write(bind_content)
                print(f"DOMAINS - Reloading DNS service..")
                subprocess.run(['docker', 'restart', 'openpanel_dns'])
                flash(f'Zone file for {domain_name} saved successfully and DNS service reloaded.', 'success')
            except Exception as e:
                print(f"DOMAINS - Error saving DNS file: {e}")
                flash(f'Error saving DNS zone file for {domain_name}.', 'error')

        if os.path.exists(bind_file_path):
            with open(bind_file_path, 'r') as file:
                bind_content = file.read()
        else:
            print(f"DOMAINS - Error reading DNS zone file for {domain_name}")
            flash(f'Error reading DNS zone file for {domain_name}.', 'error')
            bind_content = ''

    else:
        domain_name = None
        bind_content = None
        domains = get_all_domains()
    return render_template('domains/dns-zone-editor.html', title='DNS Zone Editor', current_route=current_route, domains=domains, domain_name=domain_name, bind_content=bind_content)






# edit caddy files domains
@app.route('/domains/caddy', methods=['GET'])
@app.route('/domains/caddy/<domain_name>', methods=['GET', 'POST'])
@admin_required_route
def edit_domains_caddy_files(domain_name=None):
    current_route = request.path
    if domain_name:
        domains = []

        bind_file_path = f'/etc/openpanel/caddy/domains/{domain_name}.conf'

        if request.method == 'POST':
            bind_content = request.form.get('bind_content') #request.json['new_content']
            try:
                print(f"DOMAINS - Updating: {bind_file_path}")
                with open(bind_file_path, 'w') as file:
                    file.write(bind_content)
                print(f"DOMAINS - Restarting Caddy webserver..")
                subprocess.run(['docker', 'restart', 'caddy'])
                flash(f'Caddyfile for domain {domain_name} saved successfully and Caddy webserver reloaded.', 'success')
            except Exception as e:
                print(f"DOMAINS - Error saving Caddyfile for {domain_name}: {e}")
                flash(f'Error saving Caddyfile for {domain_name}.', 'error')

        if os.path.exists(bind_file_path):
            print(f"DOMAINS - Reading: {bind_file_path}")
            with open(bind_file_path, 'r') as file:
                bind_content = file.read()
        else:
            print(f"DOMAINS - Error reading Caddy file for domain {domain_name}")
            flash(f'Error reading Caddy file for domain {domain_name}.', 'error')
            bind_content = ''

    else:
        domain_name = None
        bind_content = None
        domains = get_all_domains()
    return render_template('domains/caddy-editor.html', title='Edit Domain Caddyfile', current_route=current_route, domains=domains, domain_name=domain_name, bind_content=bind_content)






# helper
def get_webserver_for(context):
    env_file_path = f'/home/{context}/.env'
    print(f"DOMAINS - Checking webserver configured in: {env_file_path}")

    try:
        with open(f'/home/{context}/.env', 'r') as file:
            lines = file.readlines()
            for line in lines:
                if line.strip() and not line.startswith('#'):
                    key, value = line.split('=', 1)
                    key = key.strip()
                    value = value.strip().strip('\'"')  # Strip single and double quotes
                    if key == 'WEB_SERVER':
                        return value
    except FileNotFoundError:
        print(f"DOMAINS - Fatal error - User file is missing: {env_file_path}")
        return f".env file not found."
    return None



# edit virtualhosts files domains
@app.route('/domains/vhost', methods=['GET'])
@app.route('/domains/vhost/<username>/<domain_name>', methods=['GET', 'POST'])
@admin_required_route
def edit_domains_vhost_files(username=None, domain_name=None):
    current_route = request.path
    if domain_name and username:
        domains = []
        context = query_context_by_username(username) 
        bind_file_path = f'/home/{context}/docker-data/volumes/{context}_webserver_data/_data/{domain_name}.conf'

        if request.method == 'POST':
            bind_content = request.form.get('bind_content') #request.json['new_content']
            try:
                print(f"DOMAINS - Writing VHost to: {bind_file_path}")
                with open(bind_file_path, 'w') as file:
                    file.write(bind_content)

                webserver = get_webserver_for(context)
                print(f"DOMAINS - Restarting user webserver: {webserver}")
                subprocess.run(['docker', f'--context={context}' 'restart', webserver])
                flash(f'VirtualHosts file for {domain_name} saved successfully and {webserver} restarted.', 'success')
            except Exception as e:
                print(f"DOMAINS - Error saving VHost {e}")
                flash(f'Error saving VirtualHosts file for {domain_name}.', 'error')

        if os.path.exists(bind_file_path):
            print(f"DOMAINS - Reading: {bind_file_path}")

            with open(bind_file_path, 'r') as file:
                bind_content = file.read()
        else:
            print(f"DOMAINS - Error reading VirtualHosts file for domain {domain_name}")
            flash(f'Error reading VirtualHosts file for domain {domain_name}.', 'error')
            bind_content = ''

    else:
        domain_name = None
        bind_content = None
        domains = get_all_domains()
    return render_template('domains/vhost-editor.html', title='Edit Domain VirtualHosts file', current_route=current_route, domains=domains, domain_name=domain_name, bind_content=bind_content)



@app.route('/domains/log', methods=['GET'])
@app.route('/domains/log/', methods=['GET'])
@app.route('/domains/log/<domain_name>', methods=['GET'])
@admin_required_route
#@cache.memoize(timeout=300)
def view_domain_access_log(domain_name=None, page=1):
    current_route = request.path

    if domain_name:
        log_file_path = f'/var/log/caddy/domlogs/{domain_name}/access.log'

        if os.path.exists(log_file_path):
            print(f"DOMAINS - Reading: {log_file_path}")

            try:
                if os.path.getsize(log_file_path) == 0:
                    flash(f"Log file for domain {domain_name} is empty.", "info")
                    return redirect(url_for('view_domain_access_log'))

                with open(log_file_path, 'r') as file:
                    json_logs = [json.loads(line) for line in file]
                    json_logs.reverse()

                total_logs = len(json_logs)

                show_all = request.args.get('show_all')
                if show_all == 'true':
                    items_per_page = total_logs
                    total_pages = 1
                else:
                    items_per_page = 1000
                    total_pages = total_logs // items_per_page
                    if total_logs % items_per_page != 0:
                        total_pages += 1

                total_allowed_lines_for_show_all = 10000
                    
                    
                # Paginate logs
                page = request.args.get('page', default=1, type=int)
                start_idx = (page - 1) * items_per_page
                end_idx = start_idx + items_per_page
                paginated_content = json_logs[start_idx:end_idx]

                return render_template(
                        'domains/logs.html', show_all=show_all, json_logs=paginated_content, title=f'{domain_name} Access Log',
 domain_name=domain_name, current_route=current_route, current_page=page, items_per_page=items_per_page, total_pages=total_pages, total_lines=total_logs, total_allowed_lines_for_show_all=total_allowed_lines_for_show_all
                    )
            except Exception as e:
                print(f"DOMAINS - Error reading log file: {str(e)}")
                flash(f"Error reading log file: {str(e)}", "danger")
                return redirect(url_for('view_domain_access_log'))
        else:
            print(f"DOMAINS - Log file not found for domain {domain_name}")
            flash(f"Log file not found for domain {domain_name}.", 'error')
            return redirect(url_for('view_domain_access_log'))

    else:
        domains = get_all_domains()
        return render_template('domains/logs.html', title='Access Logs', current_route=current_route, domains=domains)





@app.route('/domains/stats/<current_username>/<domain_name>', methods=['GET'])
@admin_required_route
@cache.memoize(timeout=7200)
def view_domain_stats_file(current_username, domain_name):
    current_route = request.path

    log_file_path = f'/var/log/caddy/stats/{current_username}/{domain_name}.html'

    if os.path.exists(log_file_path):
        print(f"DOMAINS - Reading: {log_file_path}")
        try:
            with open(log_file_path, 'r') as file:
                html_content = file.read()

            return render_template('domains/goaccess_single.html', html_content=html_content, title=f'{domain_name} GoAccess Log', domain_name=domain_name, current_route=current_route)

        except FileNotFoundError:
            print(f"DOMAINS - File does not exist: {log_file_path}")
            flash(f"Stats file for domain {domain_name} not found. Data is generated every 24h.", 'error')
            return redirect(url_for('domains'))
    else:
        print(f"DOMAINS - File does not exist: {log_file_path}")
        flash(f"Stats file for domain {domain_name} not found. Data is generated every 24h.", 'error')
        return redirect(url_for('domains'))


