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


# TODO: admin still shows old time after timezone change, needs reload

# import python modules
from flask import Flask, render_template, request, redirect, url_for, flash
import subprocess
import pytz

# import our modules
from app import app, login_required_route



@app.route('/server/timezone', methods=['GET', 'POST'])
@login_required_route
def server_timezone_settings():
    selected_zone_goes_here = request.form.get('timezone')

    # List available timezones
    available_timezones = pytz.all_timezones

    # Construct the Docker command to read conf file
    command_read_current_zone = f'cat /etc/timezone'

    result = subprocess.run(command_read_current_zone, shell=True, stdout=subprocess.PIPE, stderr=subprocess.PIPE, text=True)

    if result.returncode != 0:
        return jsonify({'error': f'Error reading current timezone from the server: {result.stderr}'}), 500

    current_timezone = result.stdout

    # List zones
    command_to_change_timezone = f'/bin/ln -fs /usr/share/zoneinfo/{selected_zone_goes_here} /etc/localtime && dpkg-reconfigure -f noninteractive tzdata'
    if request.method == 'POST':
 
        write_result = subprocess.run(command_to_change_timezone, shell=True, stdout=subprocess.PIPE, stderr=subprocess.PIPE, text=True)

        if write_result.returncode != 0:
            return jsonify({'error': f'Error changing timezone to {selected_zone_goes_here}: {write_result.stderr}'}), 500

        return redirect(url_for('server_timezone_settings'))

    return render_template('server/timezone.html', available_timezones=available_timezones, current_timezone=current_timezone)
