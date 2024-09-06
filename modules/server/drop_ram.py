################################################################################
# Author: Stefan Pejcic
# Created: 01.07.2024
# Last Modified: 01.07.2024
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
from flask import Flask, jsonify
import os

# import our modules
from app import app, login_required_route



def drop_cached_ram():
    try:
        os.system('sync; echo 3 > /proc/sys/vm/drop_caches')
        return True
    except Exception as e:
        print(f"Error dropping cache: {e}")
        return False

@app.route('/server/memory_usage/drop', methods=['POST'])
@login_required_route
def drop_memory_cache():
    if drop_cached_ram():
        return jsonify({"status": "success", "message": "Cache dropped successfully."}), 200
    else:
        return jsonify({"status": "error", "message": "Failed to drop cache."}), 500
