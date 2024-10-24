local context = std.extVar('context');

local controllerImage = context.images.machineAPIOperator;
local rbacProxyImage = context.images.kubeRBACProxy;

local kubeProxyContainer = function(upstreamPort, portName, exposePort) {
  args: [
    '--secure-listen-address=0.0.0.0:%s' % exposePort,
    '--upstream=http://localhost:%s' % upstreamPort,
    '--config-file=/etc/kube-rbac-proxy/config-file.yaml',
    '--tls-cert-file=/etc/tls/private/tls.crt',
    '--tls-private-key-file=/etc/tls/private/tls.key',
    '--tls-cipher-suites=TLS_ECDHE_RSA_WITH_AES_128_GCM_SHA256,TLS_ECDHE_ECDSA_WITH_AES_128_GCM_SHA256,TLS_ECDHE_RSA_WITH_AES_256_GCM_SHA384,TLS_ECDHE_ECDSA_WITH_AES_256_GCM_SHA384,TLS_ECDHE_RSA_WITH_CHACHA20_POLY1305,TLS_ECDHE_ECDSA_WITH_CHACHA20_POLY1305',
    '--logtostderr=true',
    '--v=3',
  ],
  image: rbacProxyImage,
  imagePullPolicy: 'IfNotPresent',
  name: 'kube-rbac-proxy-%s' % portName,
  ports: [
    {
      containerPort: exposePort,
      name: portName,
      protocol: 'TCP',
    },
  ],
  resources: {
    requests: {
      cpu: '10m',
      memory: '20Mi',
    },
  },
  terminationMessagePath: '/dev/termination-log',
  terminationMessagePolicy: 'File',
  volumeMounts: [
    {
      mountPath: '/etc/kube-rbac-proxy',
      name: 'config',
    },
    {
      mountPath: '/etc/tls/private',
      name: 'machine-api-controllers-tls',
    },
  ],
};


local controllersDeployment = {
  apiVersion: 'apps/v1',
  kind: 'Deployment',
  metadata: {
    annotations: {},
    labels: {
      api: 'clusterapi',
      'k8s-app': 'controller',
    },
    name: 'appuio-machine-api-controllers',
  },
  spec: {
    progressDeadlineSeconds: 600,
    replicas: 1,
    revisionHistoryLimit: 10,
    selector: {
      matchLabels: {
        api: 'clusterapi',
        'k8s-app': 'controller',
      },
    },
    strategy: {
      rollingUpdate: {
        maxSurge: '25%',
        maxUnavailable: '25%',
      },
      type: 'RollingUpdate',
    },
    template: {
      metadata: {
        annotations: {
          'target.workload.openshift.io/management': '{"effect": "PreferredDuringScheduling"}',
        },
        creationTimestamp: null,
        labels: {
          api: 'clusterapi',
          'k8s-app': 'controller',
        },
      },
      spec: {
        containers: [
          {
            args: [
              '--logtostderr=true',
              '--v=3',
              '--leader-elect=true',
              '--leader-elect-lease-duration=120s',
              '--namespace=openshift-machine-api',
            ],
            command: [
              '/machineset-controller',
            ],
            image: controllerImage,
            imagePullPolicy: 'IfNotPresent',
            livenessProbe: {
              failureThreshold: 3,
              httpGet: {
                path: '/readyz',
                port: 'healthz',
                scheme: 'HTTP',
              },
              periodSeconds: 10,
              successThreshold: 1,
              timeoutSeconds: 1,
            },
            name: 'machineset-controller',
            ports: [
              {
                containerPort: 8443,
                name: 'webhook-server',
                protocol: 'TCP',
              },
              {
                containerPort: 9441,
                name: 'healthz',
                protocol: 'TCP',
              },
            ],
            readinessProbe: {
              failureThreshold: 3,
              httpGet: {
                path: '/healthz',
                port: 'healthz',
                scheme: 'HTTP',
              },
              periodSeconds: 10,
              successThreshold: 1,
              timeoutSeconds: 1,
            },
            resources: {
              requests: {
                cpu: '10m',
                memory: '20Mi',
              },
            },
            terminationMessagePath: '/dev/termination-log',
            terminationMessagePolicy: 'File',
            volumeMounts: [
              {
                mountPath: '/etc/machine-api-operator/tls',
                name: 'machineset-webhook-cert',
                readOnly: true,
              },
            ],
          },
          {
            args: [
              '--logtostderr=true',
              '--v=3',
              '--leader-elect=true',
              '--leader-elect-lease-duration=120s',
              '--namespace=openshift-machine-api',
            ],
            command: [
              '/nodelink-controller',
            ],
            image: controllerImage,
            imagePullPolicy: 'IfNotPresent',
            name: 'nodelink-controller',
            resources: {
              requests: {
                cpu: '10m',
                memory: '20Mi',
              },
            },
            terminationMessagePath: '/dev/termination-log',
            terminationMessagePolicy: 'File',
          },
          {
            args: [
              '--logtostderr=true',
              '--v=3',
              '--leader-elect=true',
              '--leader-elect-lease-duration=120s',
              '--namespace=openshift-machine-api',
            ],
            command: [
              '/machine-healthcheck',
            ],
            image: controllerImage,
            imagePullPolicy: 'IfNotPresent',
            livenessProbe: {
              failureThreshold: 3,
              httpGet: {
                path: '/readyz',
                port: 'healthz',
                scheme: 'HTTP',
              },
              periodSeconds: 10,
              successThreshold: 1,
              timeoutSeconds: 1,
            },
            name: 'machine-healthcheck-controller',
            ports: [
              {
                containerPort: 9442,
                name: 'healthz',
                protocol: 'TCP',
              },
            ],
            readinessProbe: {
              failureThreshold: 3,
              httpGet: {
                path: '/healthz',
                port: 'healthz',
                scheme: 'HTTP',
              },
              periodSeconds: 10,
              successThreshold: 1,
              timeoutSeconds: 1,
            },
            resources: {
              requests: {
                cpu: '10m',
                memory: '20Mi',
              },
            },
            terminationMessagePath: '/dev/termination-log',
            terminationMessagePolicy: 'File',
          },
          kubeProxyContainer('8082', 'machineset-mtrc', 8442),
          kubeProxyContainer('8081', 'machine-mtrc', 8441),
          kubeProxyContainer('8083', 'mhc-mtrc', 8444),
        ],
        dnsPolicy: 'ClusterFirst',
        nodeSelector: {
          'node-role.kubernetes.io/master': '',
        },
        priorityClassName: 'system-node-critical',
        restartPolicy: 'Always',
        schedulerName: 'default-scheduler',
        securityContext: {},
        serviceAccount: 'machine-api-controllers',
        serviceAccountName: 'machine-api-controllers',
        terminationGracePeriodSeconds: 30,
        tolerations: [
          {
            effect: 'NoSchedule',
            key: 'node-role.kubernetes.io/master',
          },
          {
            key: 'CriticalAddonsOnly',
            operator: 'Exists',
          },
          {
            effect: 'NoExecute',
            key: 'node.kubernetes.io/not-ready',
            operator: 'Exists',
            tolerationSeconds: 120,
          },
          {
            effect: 'NoExecute',
            key: 'node.kubernetes.io/unreachable',
            operator: 'Exists',
            tolerationSeconds: 120,
          },
        ],
        volumes: [
          {
            name: 'machineset-webhook-cert',
            secret: {
              defaultMode: 420,
              items: [
                {
                  key: 'tls.crt',
                  path: 'tls.crt',
                },
                {
                  key: 'tls.key',
                  path: 'tls.key',
                },
              ],
              secretName: 'machine-api-operator-webhook-cert',
            },
          },
          {
            configMap: {
              defaultMode: 420,
              name: 'kube-rbac-proxy',
            },
            name: 'config',
          },
          {
            name: 'machine-api-controllers-tls',
            secret: {
              defaultMode: 420,
              secretName: 'machine-api-controllers-tls',
            },
          },
        ],
      },
    },
  },
};

controllersDeployment
