FROM lambci/lambda:build

RUN npm install -g yarn

COPY worker /var/task/worker

CMD ["node", "/var/task/worker/index.js"]
