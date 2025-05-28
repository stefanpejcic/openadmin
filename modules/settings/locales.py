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
# Created: 11.07.2023
# Last Modified: 09.03.2024
# Company: OPENPANEL
# Copyright (c) openpanel.com
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
from flask import Flask, Response, abort, render_template, request, send_file, g, jsonify, session, url_for, flash, redirect, get_flashed_messages

import requests
import os
import re
import subprocess

# import our functions
from app import app, cache, is_license_valid, admin_required_route, load_openpanel_config, connect_to_database

github_repo_for_locales = "https://api.github.com/repos/stefanpejcic/openpanel-translations/contents"
translations_dir = "/etc/openpanel/openpanel/translations"

# LOCALES PAGE
@app.route('/settings/locales', methods=['GET', 'POST'])
@admin_required_route
def locales():
    current_route = request.path

    if request.method == 'POST':
        locale_to_install = request.form.get('locale') or request.json.get('locale')

        if not locale_to_install:
            return jsonify({"error": "Missing 'locale' in request."}), 400

        if not re.match(r'^[a-z]{2,3}-[a-z]{2,3}$', locale_to_install, re.IGNORECASE):
            return jsonify({"error": "Invalid locale format."}), 400

        try:
            subprocess.run(["opencli", "locale", locale_to_install], check=True)
            if request.is_json:
                return jsonify({"message": f"Locale '{locale_to_install}' installed successfully."}), 200
            else:
                flash(f"Locale '{locale_to_install}' installed successfully and is now available to users.", "success")
                return redirect(url_for('locales'))
        except subprocess.CalledProcessError as e:
            error_msg = f"Failed to install locale: {e}"
            if request.is_json:
                return jsonify({"error": error_msg}), 500
            else:
                flash(error_msg, "error")
                return redirect(url_for('locales'))

    # GET
    response = requests.get(github_repo_for_locales)
    if response.status_code != 200:
        return f"Failed to fetch data from GitHub: {response.status_code}", 500

    folders = response.json()
    results = []

    for item in folders:
        if item["type"] == "dir":
            folder_name = item["name"]
            base_name = folder_name.split("-")[0]
            local_path = os.path.join(translations_dir, base_name)
            exists_locally = os.path.isdir(local_path)

            results.append({
                "locale": folder_name,
                "provider": 'OpenPanel' if base_name == 'en' else 'Community',
                "path": local_path if exists_locally else '',
                "installed": exists_locally
            })

    if request.args.get('output') == 'json':
        return jsonify(results)

    return render_template('settings/translations.html', title='Settings', current_route=current_route, translations=results)
