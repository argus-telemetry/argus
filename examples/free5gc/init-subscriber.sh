#!/bin/sh
# Provision a test subscriber via free5GC WebUI API.
# Run after the core is fully started.

WEBUI_URL="${WEBUI_URL:-http://localhost:5001}"
MAX_RETRIES=30
RETRY_INTERVAL=5

echo "Waiting for WebUI at $WEBUI_URL..."
for i in $(seq 1 $MAX_RETRIES); do
  if curl -sf "$WEBUI_URL" > /dev/null 2>&1; then
    echo "WebUI is up."
    break
  fi
  echo "  Attempt $i/$MAX_RETRIES — retrying in ${RETRY_INTERVAL}s..."
  sleep $RETRY_INTERVAL
done

# Login to get JWT token.
TOKEN=$(curl -sf -X POST "$WEBUI_URL/api/login" \
  -H "Content-Type: application/json" \
  -d '{"username":"admin","password":"free5gc"}' | python3 -c "import sys,json; print(json.load(sys.stdin).get('access_token',''))" 2>/dev/null)

if [ -z "$TOKEN" ]; then
  echo "WARNING: Could not authenticate with WebUI. Subscriber provisioning skipped."
  echo "You may need to create the subscriber manually via http://localhost:5000"
  exit 0
fi

echo "Provisioning subscriber IMSI-208930000000001..."
HTTP_CODE=$(curl -sf -o /dev/null -w "%{http_code}" -X POST "$WEBUI_URL/api/subscriber/imsi-208930000000001/20893" \
  -H "Content-Type: application/json" \
  -H "Token: $TOKEN" \
  -d '{
    "plmnID": "20893",
    "ueId": "imsi-208930000000001",
    "AuthenticationSubscription": {
      "authenticationManagementField": "8000",
      "authenticationMethod": "5G_AKA",
      "milenage": {
        "op": {
          "encryptionAlgorithm": 0,
          "encryptionKey": 0,
          "opValue": ""
        }
      },
      "opc": {
        "encryptionAlgorithm": 0,
        "encryptionKey": 0,
        "opcValue": "8e27b6af0e692e750f32667a3b14605d"
      },
      "permanentKey": {
        "encryptionAlgorithm": 0,
        "encryptionKey": 0,
        "permanentKeyValue": "8baf473f2f8fd09487cccbd7097c6862"
      },
      "sequenceNumber": "000000000020"
    },
    "AccessAndMobilitySubscriptionData": {
      "gpsis": ["msisdn-0900000000"],
      "nssai": {
        "defaultSingleNssais": [
          {
            "sst": 1,
            "sd": "010203"
          }
        ]
      },
      "subscribedUeAmbr": {
        "downlink": "2 Gbps",
        "uplink": "1 Gbps"
      }
    },
    "SessionManagementSubscriptionData": [
      {
        "singleNssai": {
          "sst": 1,
          "sd": "010203"
        },
        "dnnConfigurations": {
          "internet": {
            "pduSessionTypes": {
              "defaultSessionType": "IPV4",
              "allowedSessionTypes": ["IPV4"]
            },
            "sscModes": {
              "defaultSscMode": "SSC_MODE_1",
              "allowedSscModes": ["SSC_MODE_2", "SSC_MODE_3"]
            },
            "5gQosProfile": {
              "5qi": 9,
              "arp": {
                "priorityLevel": 8,
                "preemptCap": "",
                "preemptVuln": ""
              }
            },
            "sessionAmbr": {
              "downlink": "1000 Mbps",
              "uplink": "1000 Mbps"
            }
          }
        }
      }
    ],
    "SmfSelectionSubscriptionData": {
      "subscribedSnssaiInfos": {
        "01010203": {
          "dnnInfos": [
            {
              "dnn": "internet"
            }
          ]
        }
      }
    },
    "AmPolicyData": {
      "subscCats": ["free5gc"]
    },
    "SmPolicyData": {
      "smPolicySnssaiData": {
        "01010203": {
          "snssai": {
            "sst": 1,
            "sd": "010203"
          },
          "smPolicyDnnData": {
            "internet": {
              "dnn": "internet"
            }
          }
        }
      }
    }
  }')

if [ "$HTTP_CODE" = "201" ] || [ "$HTTP_CODE" = "200" ]; then
  echo "Subscriber provisioned successfully."
elif [ "$HTTP_CODE" = "409" ]; then
  echo "Subscriber already exists."
else
  echo "WARNING: Subscriber provisioning returned HTTP $HTTP_CODE"
fi
