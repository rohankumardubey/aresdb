package gateway

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"time"

	mux "github.com/gorilla/mux"
	"github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	"github.com/uber/aresdb/metastore/common"
)

var _ = ginkgo.Describe("Controller", func() {
	var testServer *httptest.Server
	var hostPort string

	headers := http.Header{
		"Foo": []string{"bar"},
	}

	table := common.Table{
		Version: 0,
		Name:    "test1",
		Columns: []common.Column{
			{
				Name: "col1",
				Type: "int32",
			},
		},
	}
	tableBytes, _ := json.Marshal(table)
	tables := []common.Table{
		table,
	}

	column2EnumCases := []string{"1"}
	enumCasesBytes, _ := json.Marshal(column2EnumCases)
	column2extendedEnumIDs := []int{2}
	enumIDBytes, _ := json.Marshal(column2extendedEnumIDs)

	ginkgo.BeforeEach(func() {
		testRouter := mux.NewRouter()
		testServer = httptest.NewUnstartedServer(testRouter)
		testRouter.HandleFunc("/schema/ns1/tables", func(w http.ResponseWriter, r *http.Request) {
			b, _ := json.Marshal(tables)
			w.Write(b)
		})
		testRouter.HandleFunc("/schema/ns_baddata/tables", func(w http.ResponseWriter, r *http.Request) {
			w.Write([]byte(`"bad data`))
		})
		testRouter.HandleFunc("/schema/ns1/hash", func(w http.ResponseWriter, r *http.Request) {
			w.Write([]byte("123"))
		})
		testRouter.HandleFunc("/assignment/ns1/hash/0", func(w http.ResponseWriter, r *http.Request) {
			w.Write([]byte("123"))
		})
		testRouter.HandleFunc("/assignment/ns1/assignments/0", func(w http.ResponseWriter, r *http.Request) {
			w.Write([]byte(`
{  
   "subscriber":"0",
   "jobs":[  
      {  
         "job":"client_info_test_1",
         "version":1,
         "aresTableConfig":{  
            "name":"client_info_test_1",
            "cluster":"",
            "schema":{  
               "name":"",
               "columns":null,
               "primaryKeyColumns":null,
               "isFactTable":false,
               "config":{  

               },
               "version":0
            }
         },
         "streamConfig":{  
            "topic":"hp-styx-rta-client_info",
            "kafkaClusterName":"kloak-sjc1-lossless",
            "kafkaClusterFile":"clusters.yaml",
            "topicType":"heatpipe",
            "lastestOffset":true,
            "errorThreshold":10,
            "statusCheckInterval":60,
            "autoRecoveryThreshold":8,
            "processorCount":1,
            "batchSize":32768,
            "maxBatchDelayMS":10000,
            "megaBytePerSec":600,
            "restartOnFailure":true,
            "restartInterval":300,
            "failureHandler":{  
               "type":"retry",
               "config":{  
                  "initRetryIntervalInSeconds":60,
                  "multiplier":1,
                  "maxRetryMinutes":525600
               }
            }
         }
      }
   ]
}`))
		})
		testRouter.HandleFunc("/schema/ns1/tables/test1", func(w http.ResponseWriter, r *http.Request) {
			w.Write(tableBytes)
		})
		testRouter.HandleFunc("/enum/ns1/test1/columns/col2/enum-cases", func(w http.ResponseWriter, r *http.Request) {
			if r.Method == http.MethodGet {
				w.Write(enumCasesBytes)
			} else if r.Method == http.MethodPost {
				w.Write(enumIDBytes)
			}
		})
		testRouter.HandleFunc("/schema/ns_baddata/tables/test1", func(w http.ResponseWriter, r *http.Request) {
			w.Write(enumCasesBytes)
		})
		testRouter.HandleFunc("/enum/ns_baddata/test1/columns/col2/enum-cases", func(w http.ResponseWriter, r *http.Request) {
			if r.Method == http.MethodGet {
				w.Write(enumIDBytes)
			} else if r.Method == http.MethodPost {
				w.Write(enumCasesBytes)
			}
		})
		testServer.Start()
		hostPort = testServer.Listener.Addr().String()
	})

	ginkgo.AfterEach(func() {
		testServer.Close()
	})

	ginkgo.It("NewControllerHTTPClient should work", func() {
		c := NewControllerHTTPClient(hostPort, 20*time.Second, headers)
		??(c.address).Should(Equal(hostPort))
		??(c.headers).Should(Equal(headers))

		hash, err := c.GetSchemaHash("ns1")
		??(err).Should(BeNil())
		??(hash).Should(Equal("123"))

		tablesGot, err := c.GetAllSchema("ns1")
		??(err).Should(BeNil())
		??(tablesGot).Should(Equal(tables))

		hash, err = c.GetAssignmentHash("ns1", "0")
		??(err).Should(BeNil())
		??(hash).Should(Equal("123"))

		_, err = c.GetAssignment("ns1", "0")
		??(err).Should(BeNil())

		c.SetNamespace("ns1")
		tablesGot, err = c.FetchAllSchemas()
		??(err).Should(BeNil())
		??(tablesGot).Should(Equal(tables))

		tableGot, err := c.FetchSchema("test1")
		??(err).Should(BeNil())
		??(*tableGot).Should(Equal(table))

		enumCasesGot, err := c.FetchAllEnums("test1", "col2")
		??(err).Should(BeNil())
		??(enumCasesGot).Should(Equal(column2EnumCases))

		column2extendedEnumIDsGot, err := c.ExtendEnumCases("test1", "col2", []string{"2"})
		??(err).Should(BeNil())
		??(column2extendedEnumIDsGot).Should(Equal(column2extendedEnumIDs))
	})

	ginkgo.It("should fail with errors", func() {
		c := NewControllerHTTPClient(hostPort, 2*time.Second, headers)
		_, err := c.GetSchemaHash("bad_ns")
		??(err).ShouldNot(BeNil())
		tablesGot, err := c.GetAllSchema("bad_ns")
		??(err).ShouldNot(BeNil())
		??(tablesGot).Should(BeNil())
		c.SetNamespace("bad_ns")
		_, err = c.FetchAllSchemas()
		??(err).ShouldNot(BeNil())
		_, err = c.FetchAllEnums("test1", "col1")
		??(err).ShouldNot(BeNil())
		_, err = c.FetchAllEnums("test1", "col2")
		??(err).ShouldNot(BeNil())
		_, err = c.ExtendEnumCases("test1", "col2", []string{"2"})
		??(err).ShouldNot(BeNil())

		_, err = c.GetAllSchema("ns_baddata")
		??(err).ShouldNot(BeNil())
		c.SetNamespace("ns_baddata")
		_, err = c.FetchAllSchemas()
		??(err).ShouldNot(BeNil())
		_, err = c.FetchAllEnums("test1", "col1")
		??(err).ShouldNot(BeNil())
		_, err = c.FetchAllEnums("test1", "col2")
		??(err).ShouldNot(BeNil())
		_, err = c.ExtendEnumCases("test1", "col2", []string{"2"})
		??(err).ShouldNot(BeNil())
	})

	ginkgo.It("buildRequest should work", func() {
		c := NewControllerHTTPClient(hostPort, 20*time.Second, headers)
		headerLen := len(c.headers)
		req, err := c.buildRequest(http.MethodGet, "somepath", nil)
		??(err).Should(BeNil())
		??(req.Header).Should(HaveLen(2))
		??(c.headers).Should(HaveLen(headerLen))
	})
})
