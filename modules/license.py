################################################################################
# *************************************************************************
# *                                                                       *
# * OpenAdmin                                                             *
# * Copyright (c) OpenPanel. All Rights Reserved.                         *
# * Version: 1.3.2                                                        *
# * Build Date: 2025-05-27 19:36:19                                       *
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
# Last Modified: 11.07.2024
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
from flask import Flask, Response, render_template, request, g, jsonify, session, redirect, url_for, flash
import subprocess

# import our modules
from app import app, cache, login_required_route 


@app.route('/license')
@login_required_route
@cache.cached(timeout=300)
def license():
    key=get_license_key_on_route()

    output_param = request.args.get('output')
    if output_param == 'json':
        return jsonify({'key': key})

    return render_template('license.html', title="License", key=key)



@app.route('/license/key', methods=['GET', 'POST'])
@login_required_route
@cache.cached(timeout=60)
def license_key():
    if request.method == 'GET':
        return get_license_key()
    elif request.method == 'POST':
        return post_license_key()


def get_license_key_on_route():
    try:
        result = subprocess.run(['opencli', 'license', 'key', '--json'], capture_output=True, text=True, check=True)
        output = result.stdout.strip()
        return output
    except subprocess.CalledProcessError as e:
        return None

def get_license_key():
    try:
        result = subprocess.run(['opencli', 'license', 'key', '--json'], capture_output=True, text=True, check=True)
        output = result.stdout.strip()
        return jsonify({"key": output})
    except subprocess.CalledProcessError as e:
        return jsonify({"error": str(e), "output": e.output}), 500

def post_license_key():
    data = request.get_json()
    key = data.get("key")
    if not key:
        return jsonify({"error": "Missing key in request"}), 400
    try:
        result = subprocess.run(['opencli', 'license', key, '--json', '--no-restart'], capture_output=True, text=True, check=True)
        output = result.stdout.strip()
        return jsonify({"response": output})
    except subprocess.CalledProcessError as e:
        error_msg = f"Error running opencli: {str(e)}"
        app.logger.error(error_msg)
        return jsonify({"error": "License key validation failed"}), 500
    except Exception as e:
        app.logger.error(f"Unexpected error: {str(e)}")
        return jsonify({"error": "Unexpected error occurred"}), 500





@app.route('/license/info', methods=['GET'])
@login_required_route
@cache.cached(timeout=300)
def get_license_info():
    try:
        result = subprocess.run(['opencli', 'license', 'info', '--json'], capture_output=True, text=True, check=True)
        output = result.stdout.strip()
        return jsonify({"info": output})
    except subprocess.CalledProcessError as e:
        return jsonify({"error": str(e), "output": e.output}), 500

@app.route('/license/verify', methods=['POST'])
@login_required_route
def verify_license():
    try:
        result = subprocess.run(['opencli', 'license', 'verify', '--json'], capture_output=True, text=True, check=True)
        output = result.stdout.strip()
        return jsonify({"verify": output})
    except subprocess.CalledProcessError as e:
        return jsonify({"error": str(e), "output": e.output}), 500

@app.route('/license/delete', methods=['DELETE'])
@login_required_route
def delete_license():
    try:
        result = subprocess.run(['opencli', 'license', 'delete', '--json'], capture_output=True, text=True, check=True)
        output = result.stdout.strip()
        return jsonify({"delete": output})
    except subprocess.CalledProcessError as e:
        return jsonify({"error": str(e), "output": e.output}), 500
