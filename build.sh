#!/bin/sh

# build backend
echo "building backend"
echo "going to the backend directory first"
cd backend

echo "building docker image..."
docker build -t kryakhovskiy/etracker-backend .
r=$?

if [ "$r" -ne 0 ]; then
  echo "backend docker build failed..."
  cd ..
  exit $r
fi

# push backend
echo "pushing the newly created image to the docker registry"
docker push kryakhovskiy/etracker-backend
r=$?
if [ "$r" -ne 0 ]; then
  echo "backend docker push failed..."
  cd ..
  exit $r
fi
echo "backend build done"


# build frontend
echo "building frontend"
cd ../frontend
echo "building docker image..."
docker build -t kryakhovskiy/etracker-frontend .
r=$?
if [ "$r" -ne 0 ]; then
  echo "frontend docker build failed..."
  cd ..
  exit $r
fi

# push frontend
echo "pushing the newly created image to the docker registry"
docker push kryakhovskiy/etracker-frontend
r=$?
if [ "$r" -ne 0 ]; then
  echo "frontend docker push failed..."
  cd ..
  exit $r
fi
echo "frontend build done"
echo "build completed"
