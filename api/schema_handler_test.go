//  Copyright (c) 2017-2018 Uber Technologies, Inc.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package api

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io/ioutil"
	"net/http"
	"net/http/httptest"

	"github.com/uber/aresdb/memstore"
	memMocks "github.com/uber/aresdb/memstore/mocks"
	metaCom "github.com/uber/aresdb/metastore/common"
	"github.com/uber/aresdb/metastore/mocks"

	"github.com/gorilla/mux"
	"github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	"github.com/stretchr/testify/mock"
	"github.com/uber/aresdb/metastore"
	"github.com/uber/aresdb/utils"
)

var _ = ginkgo.Describe("SchemaHandler", func() {

	var testServer *httptest.Server
	var hostPort string
	var testTable = metaCom.Table{
		Name: "testTable",
		Columns: []metaCom.Column{
			{
				Name: "col1",
				Type: "Int32",
			},
		},
		PrimaryKeyColumns: []int{0},
	}
	var testTableSchema = memstore.TableSchema{
		EnumDicts: map[string]memstore.EnumDict{
			"testColumn": {
				ReverseDict: []string{"a", "b", "c"},
				Dict: map[string]int{
					"a": 0,
					"b": 1,
					"c": 2,
				},
			},
		},
		Schema: testTable,
	}

	testMetaStore := &mocks.MetaStore{}
	var testMemStore *memMocks.MemStore
	var schemaHandler *SchemaHandler

	ginkgo.BeforeEach(func() {
		testMemStore = CreateMemStore(&testTableSchema, 0, nil, nil)
		schemaHandler = NewSchemaHandler(testMetaStore)
		testRouter := mux.NewRouter()
		schemaHandler.Register(testRouter.PathPrefix("/schema").Subrouter())
		testServer = httptest.NewUnstartedServer(WithPanicHandling(testRouter))
		testServer.Start()
		hostPort = testServer.Listener.Addr().String()
	})

	ginkgo.AfterEach(func() {
		testServer.Close()
	})

	ginkgo.It("ListTables should work", func() {
		testMemStore.On("GetSchemas").Return(map[string]*memstore.TableSchema{"testTable": nil})
		testMetaStore.On("ListTables").Return([]string{"testTable"}, nil)
		hostPort := testServer.Listener.Addr().String()
		resp, err := http.Get(fmt.Sprintf("http://%s/schema/tables", hostPort))
		??(err).Should(BeNil())
		respBody, err := ioutil.ReadAll(resp.Body)
		??(err).Should(BeNil())
		??(resp.StatusCode).Should(Equal(http.StatusOK))
		??(respBody).Should(Equal([]byte(`["testTable"]`)))
	})

	ginkgo.It("GetTable should work", func() {
		testMetaStore.On("GetTable", "testTable").Return(&testTable, nil)
		testMetaStore.On("GetTable", "unknown").Return(nil, metastore.ErrTableDoesNotExist)
		resp, err := http.Get(fmt.Sprintf("http://%s/schema/tables/%s", hostPort, "testTable"))
		??(resp.StatusCode).Should(Equal(http.StatusOK))
		??(err).Should(BeNil())
		respBody, err := ioutil.ReadAll(resp.Body)
		??(err).Should(BeNil())
		respTable := metaCom.Table{}
		json.Unmarshal(respBody, &respTable)
		??(respTable).Should(Equal(testTableSchema.Schema))

		??(resp.StatusCode).Should(Equal(http.StatusOK))

		resp, err = http.Get(fmt.Sprintf("http://%s/schema/tables/%s", hostPort, "unknown"))
		??(err).Should(BeNil())
		??(resp.StatusCode).Should(Equal(http.StatusNotFound))
	})

	ginkgo.It("AddTable should work", func() {

		tableSchemaBytes, _ := json.Marshal(testTableSchema.Schema)

		testMetaStore.On("CreateTable", mock.Anything).Return(nil).Once()
		resp, _ := http.Post(fmt.Sprintf("http://%s/schema/tables", hostPort), "application/json", bytes.NewBuffer(tableSchemaBytes))
		??(resp.StatusCode).Should(Equal(http.StatusOK))

		var createdTableSchema *metaCom.Table
		testMetaStore.On("CreateTable", mock.Anything).Run(func(args mock.Arguments) {
			createdTableSchema = args.Get(0).(*metaCom.Table)
		}).Return(nil).Once()
		resp, _ = http.Post(fmt.Sprintf("http://%s/schema/tables", hostPort), "application/json", bytes.NewBuffer(tableSchemaBytes))
		??(resp.StatusCode).Should(Equal(http.StatusOK))
		??(createdTableSchema).ShouldNot(BeNil())
		??(createdTableSchema.Config).Should(Equal(metastore.DefaultTableConfig))

		testMetaStore.On("CreateTable", mock.Anything).Return(errors.New("Failed to create table")).Once()
		resp, _ = http.Post(fmt.Sprintf("http://%s/schema/tables", hostPort), "application/json", bytes.NewBuffer(tableSchemaBytes))
		??(resp.StatusCode).Should(Equal(http.StatusInternalServerError))

		tableSchemaBytes = []byte(`{"name": ""`)
		resp, _ = http.Post(fmt.Sprintf("http://%s/schema/tables", hostPort), "application/json", bytes.NewBuffer(tableSchemaBytes))
		??(resp.StatusCode).Should(Equal(http.StatusBadRequest))
	})

	ginkgo.It("UpdateTableConfig should work", func() {
		tableSchemaBytes, _ := json.Marshal(testTableSchema.Schema)

		testMetaStore.On("CreateTable", mock.Anything, mock.Anything).Return(nil).Once()
		resp, _ := http.Post(fmt.Sprintf("http://%s/schema/tables", hostPort), "application/json", bytes.NewBuffer(tableSchemaBytes))
		??(resp.StatusCode).Should(Equal(http.StatusOK))

		testMetaStore.On("UpdateTableConfig", mock.Anything, mock.Anything).Return(nil).Once()
		req, _ := http.NewRequest(http.MethodPut, fmt.Sprintf("http://%s/schema/tables/%s", hostPort, testTableSchema.Schema.Name), bytes.NewBuffer(tableSchemaBytes))
		resp, _ = http.DefaultClient.Do(req)
		??(resp.StatusCode).Should(Equal(http.StatusOK))

		testMetaStore.On("UpdateTableConfig", mock.Anything, mock.Anything).Return(errors.New("Failed to create table")).Once()
		req, _ = http.NewRequest(http.MethodPut, fmt.Sprintf("http://%s/schema/tables/%s", hostPort, testTableSchema.Schema.Name), bytes.NewBuffer(tableSchemaBytes))
		resp, _ = http.DefaultClient.Do(req)
		??(resp.StatusCode).Should(Equal(http.StatusInternalServerError))

		tableSchemaBytes = []byte(`{"name": ""`)
		req, _ = http.NewRequest(http.MethodPut, fmt.Sprintf("http://%s/schema/tables/%s", hostPort, testTableSchema.Schema.Name), bytes.NewBuffer(tableSchemaBytes))
		resp, _ = http.DefaultClient.Do(req)
		??(resp.StatusCode).Should(Equal(http.StatusBadRequest))
	})

	ginkgo.It("DeleteTable should work", func() {
		testMetaStore.On("DeleteTable", mock.Anything).Return(nil).Once()
		req, _ := http.NewRequest(http.MethodDelete, fmt.Sprintf("http://%s/schema/tables/%s", hostPort, "testTable"), &bytes.Buffer{})
		resp, _ := http.DefaultClient.Do(req)
		??(resp.StatusCode).Should(Equal(http.StatusOK))

		testMetaStore.On("DeleteTable", mock.Anything).Return(errors.New("Failed to delete table")).Once()
		req, _ = http.NewRequest(http.MethodDelete, fmt.Sprintf("http://%s/schema/tables/%s", hostPort, "testTable"), &bytes.Buffer{})
		resp, _ = http.DefaultClient.Do(req)
		??(resp.StatusCode).Should(Equal(http.StatusInternalServerError))
		bs, err := ioutil.ReadAll(resp.Body)
		??(err).Should(BeNil())
		defer resp.Body.Close()

		var errResp utils.APIError
		err = json.Unmarshal(bs, &errResp)
		??(err).Should(BeNil())
		??(errResp.Message).Should(Equal("Failed to delete table"))
	})

	ginkgo.It("AddColumn should work", func() {
		columnBytes := []byte(`{"name": "testCol", "type":"Int32", "defaultValue": "1"}`)
		testMetaStore.On("AddColumn", mock.Anything, mock.Anything, mock.Anything).Return(nil).Once()
		resp, _ := http.Post(fmt.Sprintf("http://%s/schema/tables/%s/columns", hostPort, "testTable"), "application/json", bytes.NewBuffer(columnBytes))
		??(resp.StatusCode).Should(Equal(http.StatusOK))

		columnBytes = []byte(`{"name": "testCol", "type":"Int32"}`)
		testMetaStore.On("AddColumn", mock.Anything, mock.Anything, mock.Anything).Return(nil).Once()
		resp, _ = http.Post(fmt.Sprintf("http://%s/schema/tables/%s/columns", hostPort, "testTable"), "application/json", bytes.NewBuffer(columnBytes))
		??(resp.StatusCode).Should(Equal(http.StatusOK))

		errorColumnBytes := []byte(`{"name": "testCol"`)
		resp, _ = http.Post(fmt.Sprintf("http://%s/schema/tables/%s/columns", hostPort, "testTable"), "application/json", bytes.NewBuffer(errorColumnBytes))
		??(resp.StatusCode).Should(Equal(http.StatusBadRequest))

		testMetaStore.On("AddColumn", mock.Anything, mock.Anything, mock.Anything).Return(errors.New("Failed to add columns")).Once()
		resp, _ = http.Post(fmt.Sprintf("http://%s/schema/tables/%s/columns", hostPort, "testTable"), "application/json", bytes.NewBuffer(columnBytes))
		??(resp.StatusCode).Should(Equal(http.StatusInternalServerError))
	})

	ginkgo.It("DeleteColumn should work", func() {
		testMetaStore.On("DeleteColumn", mock.Anything, mock.Anything).Return(nil).Once()
		req, _ := http.NewRequest(http.MethodDelete, fmt.Sprintf("http://%s/schema/tables/%s/columns/%s", hostPort, "testTable", "testColumn"), &bytes.Buffer{})
		resp, _ := http.DefaultClient.Do(req)
		??(resp.StatusCode).Should(Equal(http.StatusOK))

		testMetaStore.On("DeleteColumn", mock.Anything, mock.Anything).Return(errors.New("Failed to delete columns")).Once()
		req, _ = http.NewRequest(http.MethodDelete, fmt.Sprintf("http://%s/schema/tables/%s/columns/%s", hostPort, "testTable", "testColumn"), &bytes.Buffer{})
		resp, _ = http.DefaultClient.Do(req)
		??(resp.StatusCode).Should(Equal(http.StatusInternalServerError))
	})

	ginkgo.It("UpdateColumn should work", func() {
		testColumnConfig1 := metaCom.ColumnConfig{
			PreloadingDays: 2,
			Priority:       3,
		}

		b, err := json.Marshal(testColumnConfig1)
		??(err).Should(BeNil())

		testMetaStore.On("UpdateColumn", mock.Anything, mock.Anything, mock.Anything).
			Return(nil).Once()
		req, _ := http.NewRequest(
			http.MethodPut, fmt.Sprintf("http://%s/schema/tables/%s/columns/%s",
				hostPort, "testTable", "testColumn"), bytes.NewReader(b))
		resp, _ := http.DefaultClient.Do(req)
		??(resp.StatusCode).Should(Equal(http.StatusOK))

		testMetaStore.On("UpdateColumn", mock.Anything, mock.Anything).
			Return(errors.New("failed to update columns")).Once()
		req, _ = http.NewRequest(http.MethodPut, fmt.Sprintf("http://%s/schema/tables/%s/columns/%s",
			hostPort, "testTable", "testColumn"), bytes.NewReader(b))
		resp, _ = http.DefaultClient.Do(req)
		??(resp.StatusCode).Should(Equal(http.StatusInternalServerError))
	})
})
