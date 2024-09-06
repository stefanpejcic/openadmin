################################################################################
# Author: Stefan Pejcic
# Created: 23.05.2023
# Last Modified: 24.05.2023
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
import re
import json
from flask import Flask, Response, render_template, request, g, jsonify, session, redirect, url_for, flash
import subprocess
import datetime
from collections import OrderedDict
import psutil
from subprocess import run, Popen, PIPE

# import our modules
from app import app, is_license_valid, login_required_route, load_openpanel_config
from modules.helpers import get_all_users, get_user_and_plan_count, get_all_plans

@app.route('/settings/templates')
@login_required_route
def view_and_edit_custom_templates():
    return render_template('templates.html')
