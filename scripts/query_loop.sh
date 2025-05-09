#!/bin/bash

while true; do
    curl -X POST \
        'http://localhost:9091/api/v1/query' \
        --header 'Accept: */*' \
        --header 'User-Agent: Thunder Client (https://www.thunderclient.com)' \
        --header 'Content-Type: application/x-www-form-urlencoded' \
        --data-urlencode 'query=sum(rate(node_cpu_seconds_total{job="node",mode!="idle"}[2m])) by (instance)'
    
    echo -e "\n--- $(date) ---\n"  # Add timestamp between requests

    curl -X POST \
        'http://localhost:9091/api/v1/query' \
        --header 'Accept: */*' \
        --header 'User-Agent: Thunder Client (https://www.thunderclient.com)' \
        --header 'Content-Type: application/x-www-form-urlencoded' \
        --data-urlencode 'query=sum without (status, instance) (rate(demo_api_request_duration_seconds_count{job="demo",status=~"5.."}[1m])) / sum without (status, instance) (rate(demo_api_request_duration_seconds_count{job="demo"}[1m])) * 100 > 0.5'
    
    echo -e "\n--- $(date) ---\n"  # Add timestamp between requests

    curl -X POST \
        'http://localhost:9091/api/v1/query' \
        --header 'Accept: */*' \
        --header 'User-Agent: Thunder Client (https://www.thunderclient.com)' \
        --header 'Content-Type: application/x-www-form-urlencoded' \
        --data-urlencode 'query=sum without (instance) (up{job="demo"}) == 0'
    
    echo -e "\n--- $(date) ---\n"  # Add timestamp between requests
    sleep 10
done