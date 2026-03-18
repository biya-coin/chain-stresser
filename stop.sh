#!/bin/bash

echo "Stopping biyachaind nodes..."

for i in 0 1 2 3; do
    PID_FILE="node-log/pid$i.pid"
    if [ -f "$PID_FILE" ]; then
        PID=$(cat "$PID_FILE")
        if kill -0 "$PID" 2>/dev/null; then
            echo "Killing process $PID (Validator $i)..."
            kill "$PID"
            rm "$PID_FILE"
        else
            echo "Process $PID for Validator $i is not running."
            rm "$PID_FILE" # Clean up stale PID file
        fi
    else
        echo "PID file for Validator $i not found ($PID_FILE)."
    fi
done

echo "All nodes stopped."
