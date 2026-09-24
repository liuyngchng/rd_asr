#!/bin/bash

# stop service
docker stop rd_asr
# rm container
docker rm rd_asr

# start
docker run -d \
      -p 19010:19010 \
      -v $(pwd)/cfg.yml:/opt/asr/cfg.yml \
      -v $(pwd)/uploads:/opt/asr/uploads \
      -v $(pwd)/converted:/opt/asr/converted \
      -v $(pwd)/results:/opt/asr/results \
      --name rd_asr \
      rd_asr:1.0
