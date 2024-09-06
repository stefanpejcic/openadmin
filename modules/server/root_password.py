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
from flask import Flask, render_template, request, redirect, url_for, flash
import subprocess

# import our modules
from app import app, login_required_route


@app.route('/server/root-password', methods=['GET', 'POST'])
@login_required_route
def root_password():
    if request.method == 'POST':
        password = request.form['password']
        
        try:
            # Securely execute the command
            subprocess.run(['chpasswd'], input=f'root:{password}'.encode(), check=True)
            return redirect(url_for('root_password', message="Password changed successfully!"))
        except subprocess.CalledProcessError as e:
            return render_template('server/root_password.html', error=f"An error occurred: {e}")

    # For GET request, show the form
    return render_template('server/root_password.html')

