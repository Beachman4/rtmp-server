#!/usr/bin/env bash

sleep 10

ffmpeg -stream_loop -1 -re -i africa_toto.mp4 -c copy -f flv rtmp://35.193.201.151:1935/test