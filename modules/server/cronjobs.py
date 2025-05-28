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
import os

# import our modules
from app import app, admin_required_route

# Path to the cron file
cron_file = '/etc/cron.d/openpanel'

def is_valid_cron_line(line):
    stripped = line.strip()
    return stripped and not stripped.startswith('#') and not stripped.startswith('@')

def split_cron_line(line):
    line = line.strip()

    try:
        schedule_parts = line.split()
        if len(schedule_parts) < 6:
            return None  # Not enough parts for a valid cron line
        schedule = ' '.join(schedule_parts[0:5])
        command_part = ' '.join(schedule_parts[6:])  # skip the 5 schedule parts + 'root'
    except Exception:
        return None

    # Determine logging_enabled
    logging_enabled = False
    if '&&' in command_part:
        if '#&&' in command_part:
            logging_enabled = False
        else:
            logging_enabled = True

    # Extract actual command before '&&' or '#'
    split_match = re.search(r'\s+(&&|#)\s+', command_part)
    if split_match:
        split_index = split_match.start()
        command = command_part[:split_index].strip()
    else:
        command = command_part.strip()

    # Normalize the command
    if command.startswith('/usr/local/bin/opencli'):
        command = command.replace('/usr/local/bin/opencli', 'opencli', 1)

    elif command.startswith('/bin/bash /usr/local/admin/service/notifications.sh'):
        args = command.replace('/bin/bash /usr/local/admin/service/notifications.sh', '').strip()
        command = f'opencli sentinel {args}'.strip()

    return {
        'schedule': schedule,
        'command': command,
        'log': logging_enabled
    }

@app.route('/server/crons', methods=['GET', 'POST'])
@admin_required_route
def manage_crons():
    if request.method == 'GET':
        cron_jobs = []
        if os.path.exists(cron_file):
            with open(cron_file, 'r') as file:
                for line_number, line in enumerate(file, start=1):
                    if is_valid_cron_line(line):
                        parsed = split_cron_line(line)
                        if parsed:
                            parsed['line_number'] = line_number
                            cron_jobs.append(parsed)
        else:
            cron_jobs = None

        output_param = request.args.get('output')
        if output_param == 'json':
            return jsonify(cron_jobs)

        return render_template('server/crons.html', cron_jobs=cron_jobs, title="Cronjobs")
    
    if request.method == 'POST':
        # Retrieve the cron job data from the form (or JSON, depending on how data is sent)
        cron_data = request.form.to_dict()  # This will convert the form data into a dictionary
        
        # Loop through the cron data to extract cron schedules and logging states
        cron_jobs = {}
        for key, value in cron_data.items():
            if key.endswith("_schedule"):  # Cron schedule keys end with "_schedule"
                cron_id = key.split("_")[0]  # Extract the cron ID (e.g., 18, 19, 20)
                cron_schedule = value
                logging_key = f"{cron_id}_logging"
                cron_logging = cron_data.get(logging_key, "off")  # Default to "off" if not provided
                cron_jobs[cron_id] = {
                    "schedule": cron_schedule,
                    "logging": cron_logging
                }
        
        # Example function to add or update cron jobs in your system
        for cron_id, cron_info in cron_jobs.items():
            add_or_update_cron(cron_id, cron_info["schedule"], cron_info["logging"])

        flash('Cron jobs updated successfully', 'success')
        return redirect(url_for('manage_crons'))
        
