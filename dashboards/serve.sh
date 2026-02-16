#!/bin/bash
# Serve the dashboard directory on port 8000
echo "Serving Dashboard at http://localhost:8000/index.html"
python3 -m http.server 8000 --directory dashboards
