# umbra

IN ALPHA

Proxy TCP traffic and copy the incoming data to a shadow server.  

## Install

```
git clone github.com/stojg/umbra
cd umbra
go build
```

## Usage

```
./umbra -listen 0.0.0.0:5000 -backend production.server:5000 -shadow uat.server:5000
```

