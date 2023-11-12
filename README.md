# Peertube Autoscale Runners

This is a simple program which polls a Peertube database and watches pending transcoding jobs.
When a certain threshold is reached a script is executed, another script is exectued when runners are idle. This can be used to scale up/scale down runnners for transcoding when needed. It also emits Prometheus metrics on transcoding jobs in database.

## Usage

```
$ ./peertube-autoscale-runners --help
Usage of ./peertube-autoscale-runners:
  -db string
    	Database name (default "peertube1")
  -down string
    	Scale down command
  -host string
    	Database host (default "localhost")
  -listen-address string
    	Metrics port (default ":9042")
  -max-runners int
    	Maximum amount of runners (default 1)
  -min-pending int
    	Minimum pending jobs before scaling up (default 10)
  -min-runners int
    	Minimum amount of runners
  -password string
    	Database password
  -port int
    	Database port (default 5432)
  -reconcile duration
    	Reconcile delay (default 5m0s)
  -runner-prefix string
    	Prefix of runner name (default "runner")
  -up string
    	Scale up command
  -user string
    	Database user (default "peertube1")
```

An environment variable `RUNNER_NAME` is passed to the up and down command and consists of a given prefix followed by a number.

## References

https://github.com/Chocobozzz/PeerTube
