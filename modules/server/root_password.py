################################################################################
# *************************************************************************
# *                                                                       *
# * OpenAdmin                                                             *
# * Copyright (c) OpenPanel. All Rights Reserved.                         *
# * Version: 1.3.3                                                        *
# * Build Date: 2025-05-28 10:37:26                                       *
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
from flask import Flask, render_template, request, redirect, url_for, flash, get_flashed_messages
import subprocess
from functools import wraps

# import our modules
from app import app, admin_required_route

@app.route('/server/root-password', methods=['GET', 'POST'])
@admin_required_route
def root_password():
    if request.method == 'POST':
        password = request.form.get('password')

        if not password:
            flash('Password cannot be empty.', 'error')
            return redirect(url_for('root_password'))

        try:
            # Set the root password using `passwd` securely
            process = subprocess.run(
                ['passwd', 'root'],
                input=f'{password}\n{password}\n',  # Ensure input is a STRING, not bytes
                capture_output=True,
                text=True,  # Ensure text mode is enabled
                check=True
            )

            # Check if the password was successfully changed
            verify_process = subprocess.run(
                ['passwd', '--status', 'root'],
                capture_output=True,
                text=True,
                check=True
            )

            if 'P' in verify_process.stdout:
                flash('SSH password changed successfully!', 'success')
            else:
                flash('Password change verification failed.', 'error')

        except subprocess.CalledProcessError as e:
            flash(f'Error changing password: {e.stderr}', 'error')

        return redirect(url_for('root_password'))

    return render_template('server/root_password.html', title="Root Password")
