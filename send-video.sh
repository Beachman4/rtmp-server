#!/usr/bin/env bash

ffmpeg -re -i africa_toto.mp4 -framerate 60 -async 1 -c copy -f flv rtmp://localhost:1935/test