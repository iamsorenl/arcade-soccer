# Build from the repo root: docker build -f server/sim/Dockerfile .
# Only the game's js/ and the sim itself go in; sim imports ../../js/* verbatim.
FROM node:24-alpine
WORKDIR /app
COPY js/ js/
COPY server/sim/sim.mjs server/sim/
USER node
ENV PORT=8081
EXPOSE 8081
CMD ["node", "server/sim/sim.mjs"]
