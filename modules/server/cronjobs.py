################################################################################
# Author: Stefan Pejcic
# Created: 19.06.2024
# Last Modified: 19.06.2024
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
from flask import Flask, render_template, request, redirect, url_for, flash, jsonify
import subprocess
import re

# import our modules
from app import app, login_required_route

# Path to the cron file
cron_file = '/etc/cron.d/openpanel'

# Read initial content from the file
with open(cron_file, 'r') as file:
    initial_content = file.read()

# Helper function to parse cron job lines
def parse_cron_line(line):
    parts = line.split('&&')
    cron_info = parts[0].strip()
    logging_enabled = len(parts) > 1 and not parts[1].strip().startswith('#')
    return cron_info, logging_enabled

def is_valid_cron_line(line):
    # Check if the line starts with a number or '@'
    return re.match(r'^\s*[\d@]', line.strip()) is not None

@app.route('/server/crons', methods=['GET', 'POST'])
@login_required_route
def manage_crons():
    if request.method == 'GET':
        cron_jobs = {}
        for line in initial_content.strip().split('\n'):
            if is_valid_cron_line(line) and not line.strip().startswith('#') and not line.strip().startswith('@'):
                cron_info, logging_enabled = parse_cron_line(line)
                # Split the cron_info into time and command components
                time_components = cron_info.split()[0:5]
                command_components = cron_info.split()[5:]
                command = ' '.join(command_components)
                cron_jobs[command] = {'time': time_components, 'logging_enabled': logging_enabled}

        return render_template('server/crons.html', cron_jobs=cron_jobs)

    elif request.method == 'POST':
        # Handle form submission to update cron jobs
        updated_content = []
        cron_jobs = request.form.getlist('cron_jobs')

        for job in cron_jobs:
            time = request.form.get(job + '_time')
            enable_logging = request.form.get(job + '_logging') == 'on'

            if enable_logging:
                updated_content.append(f"{time} root {job}")
            else:
                updated_content.append(f"{time} root {job} #")

        # Save updated content to the file or wherever needed
        # For illustration, we'll just print it here
        for line in updated_content:
            print(line)

        return jsonify({'message': 'Cron jobs updated successfully'})

