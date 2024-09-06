################################################################################
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

# import our modules
from app import app, login_required_route, connect_to_database
from modules.helpers import get_all_domains


# view domains
@app.route('/domains', defaults={'domain_id': None}, methods=['GET', 'POST'])
@app.route('/domains/<domain_id>', methods=['GET'])
@login_required_route
def domains(domain_id):
    current_route = request.path

    try:
        domains = get_all_domains()

        # json
        output_param = request.args.get('output')
        if output_param == 'json':
            return jsonify({'domains': domains})
        else:
            return render_template('domains/domains.html', title='Domains', domains=domains, app=app, current_route=current_route)

    except Exception as e:
        print(f"An error occurred: {e}")

        domains = []
        new_plan_template_content = {}

        output_param = request.args.get('output')
        if output_param == 'json':
            return jsonify({'domains': domains})
        else:
            return render_template('domains/domains.html', title='Domains', domains=domains, app=app, current_route=current_route)
 




@app.route('/domains/log', methods=['GET'])
@app.route('/domains/log/', methods=['GET'])
@app.route('/domains/log/<username>/<domain_name>', methods=['GET'])
@login_required_route
def view_domain_access_log(domain_name=None,username=None):
    current_route = request.path

    if domain_name and username:
        log_file_path = f'/var/log/nginx/stats/{username}/{domain_name}.html'
        print(log_file_path)

        if os.path.exists(log_file_path):
            try:
                with open(log_file_path, 'r') as file:
                    html_content = file.read()

                return render_template('domains/logs_single.html', html_content=html_content, title='Access Log', domain_name=domain_name, current_route=current_route)

            except FileNotFoundError:
                return f"Log file for domain {domain_name} not found", 404
        else:
            return "Log file not found."

    else:
        domains = get_all_domains()

        # Determine the page number from the query parameters or default to 1
        page_number = int(request.args.get('page', 1))

        return render_template('domains/logs.html', title='Access Log', domains=domains, current_route=current_route, page=page_number)

