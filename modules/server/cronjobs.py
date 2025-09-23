################################################################################
# *************************************************************************
# *                                                                       *
# * OpenAdmin                                                             *
# * Copyright (c) OpenPanel. All Rights Reserved.                         *
# * Version: 1.6.0                                                        *
# * Build Date: 2025-09-23 11:18:56                                       *
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
    print(f"SERVER.CRONJOBS - Validating cron line: {line}")
    stripped = line.strip()
    return stripped and not stripped.startswith('#') and not stripped.startswith('@')

def split_cron_line(line):
    line = line.strip()
    print(f"SERVER.CRONJOBS - Splitting cron line to 6 parts..")

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
    split_match = re.search(r'\s*(?:#)?&&\s*', command_part)
    if split_match:
        command = command_part[:split_match.start()].strip()
    else:
        command = command_part.strip()


    # Normalize the command
    if command.startswith('/usr/local/bin/opencli'):
        command = command.replace('/usr/local/bin/opencli', 'opencli', 1)

    return {
        'schedule': schedule,
        'command': command,
        'log': logging_enabled
    }



import fileinput

def add_or_update_cron(line_number, schedule, logging_enabled):
    cron_path = '/etc/cron.d/openpanel'
    print(f"SERVER.CRONJOBS - Updating cron line nnumber: {line_number} in: {cron_path}")

    updated_lines = []
    found = False

    if not os.path.exists(cron_path):
        return

    with open(cron_path, 'r') as file:
        lines = file.readlines()

    for idx, line in enumerate(lines, start=1):
        if idx == int(line_number):
            # Replace this line
            # Extract the current command (so we don't lose it)
            parsed = split_cron_line(line)
            if not parsed:
                continue  # Skip invalid lines

            command = parsed['command']
            if command.startswith('opencli'):
                command = command.replace('opencli', '/usr/local/bin/opencli', 1)
            elif command.startswith('opencli sentinel'):
                command = command.replace('opencli sentinel', '/bin/bash /usr/local/admin/service/notifications.sh', 1)

            # Reconstruct the cron line with optional logging
            full_command = command
            if logging_enabled:
                full_command += " && echo cron executed >> /var/log/openpanel-cron.log"
            else:
                full_command += " #&& echo cron executed >> /var/log/openpanel-cron.log"

            new_line = f"{schedule} root {full_command}\n"
            updated_lines.append(new_line)
            found = True
        else:
            updated_lines.append(line)

    if found:
        with open(cron_path, 'w') as file:
            file.writelines(updated_lines)








@app.route('/server/crons', methods=['GET', 'POST'])
@admin_required_route
def manage_crons():
    if request.method == 'GET':
        cron_jobs = []
        print(f"SERVER.CRONJOBS - Reading cronjobs from: {cron_file}")
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
        cron_data = request.form.to_dict()
        cron_jobs = {}

        for key, value in cron_data.items():
            # Match keys like "18_schedule_0", "18_schedule_1", ..., "18_logging"
            schedule_match = re.match(r"(\d+)_schedule_(\d+)", key)
            logging_match = re.match(r"(\d+)_logging", key)

            if schedule_match:
                cron_id, index = schedule_match.groups()
                cron_jobs.setdefault(cron_id, {'schedule_parts': [''] * 5, 'logging': False})
                cron_jobs[cron_id]['schedule_parts'][int(index)] = value.strip()

            elif logging_match:
                cron_id = logging_match.group(1)
                cron_jobs.setdefault(cron_id, {'schedule_parts': [''] * 5, 'logging': False})
                cron_jobs[cron_id]['logging'] = True  # Checkbox is checked

        for cron_id, cron_info in cron_jobs.items():
            schedule = ' '.join(cron_info['schedule_parts'])
            logging = cron_info['logging']
            add_or_update_cron(cron_id, schedule, logging)

        flash('Cron jobs updated successfully', 'success')
        return redirect(url_for('manage_crons'))
        
