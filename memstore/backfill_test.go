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

package memstore

import (
	"github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	"github.com/stretchr/testify/mock"
	diskMocks "github.com/uber/aresdb/diskstore/mocks"
	memCom "github.com/uber/aresdb/memstore/common"
	metaCom "github.com/uber/aresdb/metastore/common"
	metaMocks "github.com/uber/aresdb/metastore/mocks"
	"github.com/uber/aresdb/utils"
	utilsMocks "github.com/uber/aresdb/utils/mocks"
	"sync"
)

var _ = ginkgo.Describe("backfill", func() {
	var tableSchema *TableSchema
	var baseBatch, newBatch *ArchiveBatch
	var upsertBatches [3]*UpsertBatch
	var patch *backfillPatch
	var hostMemoryManager memCom.HostMemoryManager
	var backfillCtx backfillContext
	var m *memStoreImpl
	table := "test"
	shardID := 0
	jobKey := getIdentifier(table, shardID, memCom.BackfillJobType)
	var scheduler *schedulerImpl
	var jobManager *backfillJobManager
	var shard *TableShard

	ginkgo.BeforeEach(func() {
		tableSchema = &TableSchema{
			Schema: metaCom.Table{
				Name: table,
				Config: metaCom.TableConfig{
					ArchivingDelayMinutes:    500,
					ArchivingIntervalMinutes: 300,
					BackfillStoreBatchSize:   20000,
				},
				IsFactTable:          true,
				ArchivingSortColumns: []int{1, 5},
				PrimaryKeyColumns:    []int{1, 2},
				Columns: []metaCom.Column{
					{Deleted: false},
					{Deleted: false}, // sort col, pk 1
					{Deleted: false}, // pk 2
					{Deleted: true},  // should skip this column.
					{Deleted: false}, // unsort col
					{Deleted: false}, // sort col, non pk
				},
			},
			PrimaryKeyBytes:   8,
			ValueTypeByColumn: []memCom.DataType{memCom.Uint32, memCom.Uint32, memCom.Uint32, memCom.Uint32, memCom.Uint32, memCom.Uint32},
			DefaultValues: []*memCom.DataValue{&memCom.NullDataValue, &memCom.NullDataValue,
				&memCom.NullDataValue, &memCom.NullDataValue, &memCom.NullDataValue, &memCom.NullDataValue},
		}

		var err error

		upsertBatches[0], err = getFactory().ReadUpsertBatch("backfill/upsertBatch0")
		??(err).Should(BeNil())
		upsertBatches[1], err = getFactory().ReadUpsertBatch("backfill/upsertBatch1")
		??(err).Should(BeNil())
		upsertBatches[2], err = getFactory().ReadUpsertBatch("backfill/upsertBatch2")
		??(err).Should(BeNil())

		patch = &backfillPatch{
			recordIDs: []RecordID{
				{0, 0},
				{0, 1},
				{0, 2},
				{1, 0},
				{1, 1},
				{1, 2},
				{2, 0},
			},
			backfillBatches: upsertBatches[:],
		}

		m = getFactory().NewMockMemStore()
		writer := new(utilsMocks.WriteCloser)
		writer.On("Write", mock.Anything).Return(0, nil)
		writer.On("Close").Return(nil)
		(m.diskStore).(*diskMocks.DiskStore).
			On("OpenVectorPartyFileForWrite",
				table, mock.Anything, shardID, 0, uint32(0), uint32(1)).Return(writer, nil)
		(m.metaStore).(*metaMocks.MetaStore).On(
			"AddArchiveBatchVersion", table, shardID,
			0, uint32(0), uint32(1), 6).Return(nil)
		(m.diskStore).(*diskMocks.DiskStore).On(
			"DeleteBatchVersions", table, shardID,
			0, uint32(0), uint32(0)).Return(nil)

		hostMemoryManager = NewHostMemoryManager(m, 1<<32)
		shard = NewTableShard(tableSchema, m.metaStore, m.diskStore, hostMemoryManager, shardID)
		batch, err := getFactory().ReadArchiveBatch("backfill/backfillBase")
		??(err).Should(BeNil())
		baseBatch = &ArchiveBatch{
			Size:  5,
			Batch: *batch,
			Shard: shard,
		}
		shard.ArchiveStore.CurrentVersion.Batches[0] = baseBatch

		batch, err = getFactory().ReadArchiveBatch("backfill/backfillNew")
		??(err).Should(BeNil())
		newBatch = &ArchiveBatch{
			Size:  6,
			Batch: *batch,
			Shard: shard,
		}

		backfillCtx = newBackfillContext(baseBatch, patch, tableSchema, tableSchema.GetColumnDeletions(),
			tableSchema.Schema.ArchivingSortColumns, tableSchema.Schema.PrimaryKeyColumns,
			tableSchema.ValueTypeByColumn, tableSchema.DefaultValues, hostMemoryManager)

		m.TableShards[table] = map[int]*TableShard{
			shardID: shard,
		}

		scheduler = newScheduler(m)
		jobManager = scheduler.jobManagers[memCom.BackfillJobType].(*backfillJobManager)
	})

	ginkgo.AfterEach(func() {
		backfillCtx.release()
	})

	ginkgo.It("createBackfillPatches should work", func() {
		var upsertBatches [3]*UpsertBatch

		// upsert batch 0
		builder := memCom.NewUpsertBatchBuilder()
		err := builder.AddColumn(0, memCom.Uint32)
		??(err).Should(BeNil())

		builder.AddRow()
		// day 0
		builder.SetValue(0, 0, uint32(0))
		builder.AddRow()
		// day 1
		builder.SetValue(1, 0, uint32(86400))
		bs, err := builder.ToByteArray()
		??(err).Should(BeNil())
		upsertBatches[0], err = NewUpsertBatch(bs)
		??(err).Should(BeNil())

		// upsert batch 1
		builder = memCom.NewUpsertBatchBuilder()
		err = builder.AddColumn(0, memCom.Uint32)
		??(err).Should(BeNil())

		builder.AddRow()
		// day 1
		builder.SetValue(0, 0, uint32(86400))

		bs, err = builder.ToByteArray()
		??(err).Should(BeNil())
		upsertBatches[1], err = NewUpsertBatch(bs)
		??(err).Should(BeNil())

		// upsert batch 2
		err = builder.AddColumn(0, memCom.Uint32)
		??(err).Should(BeNil())

		// day 2
		builder.SetValue(0, 0, uint32(2*86400))

		bs, err = builder.ToByteArray()
		??(err).Should(BeNil())
		upsertBatches[2], err = NewUpsertBatch(bs)
		??(err).Should(BeNil())

		backfillPatches, err := createBackfillPatches(upsertBatches[:], jobManager.reportBackfillJobDetail, jobKey)
		??(err).Should(BeNil())
		// day 0, 1 ,2
		??(backfillPatches).Should(HaveLen(3))
		??(backfillPatches[0].recordIDs).Should(BeEquivalentTo([]RecordID{{0, 0}}))
		??(backfillPatches[0].backfillBatches).Should(Equal(upsertBatches[:]))
		??(backfillPatches[1].recordIDs).Should(BeEquivalentTo([]RecordID{{0, 1}, {1, 0}}))
		??(backfillPatches[2].recordIDs).Should(BeEquivalentTo([]RecordID{{2, 0}}))

		scheduler.RLock()
		??(*(jobManager.getJobDetail(jobKey))).Should(Equal(BackfillJobDetail{
			JobDetail: JobDetail{
				Current:    3,
				Total:      3,
				NumRecords: 4,
			},
			Stage: "create patch",
		}))
		scheduler.RUnlock()

	})
	ginkgo.It("newBackfillStore should work", func() {
		tableSchema := &TableSchema{
			Schema: metaCom.Table{
				Name: "test",
				Config: metaCom.TableConfig{
					ArchivingDelayMinutes:    500,
					ArchivingIntervalMinutes: 300,
					BackfillStoreBatchSize:   20000,
				},
				IsFactTable:          true,
				ArchivingSortColumns: []int{1, 2},
				Columns: []metaCom.Column{
					{Deleted: false},
					{Deleted: false},
					{Deleted: false},
				},
			},
			PrimaryKeyBytes: 8,
		}

		backfillStore := newBackfillStore(tableSchema, hostMemoryManager, 0)
		??(backfillStore.Batches).ShouldNot(BeNil())
		??(backfillStore.PrimaryKey).ShouldNot(BeNil())
	})

	ginkgo.It("newBackfillContext should work", func() {
		tableSchema := &TableSchema{
			Schema: metaCom.Table{
				Name: "test",
				Config: metaCom.TableConfig{
					ArchivingDelayMinutes:    500,
					ArchivingIntervalMinutes: 300,
				},
				IsFactTable:          true,
				ArchivingSortColumns: []int{1, 2},
				PrimaryKeyColumns:    []int{1},
				Columns: []metaCom.Column{
					{Deleted: false},
					{Deleted: false},
					{Deleted: false},
				},
			},
			PrimaryKeyBytes: 8,
		}
		baseBatch := &ArchiveBatch{
			Batch: Batch{RWMutex: &sync.RWMutex{}},
		}
		patch := &backfillPatch{}
		backfillCtx := newBackfillContext(baseBatch, patch, tableSchema, tableSchema.GetColumnDeletions(),
			tableSchema.Schema.ArchivingSortColumns, tableSchema.Schema.PrimaryKeyColumns,
			tableSchema.ValueTypeByColumn, tableSchema.DefaultValues, hostMemoryManager)
		??(backfillCtx.base).Should(Equal(baseBatch))
		??(backfillCtx.patch).Should(Equal(patch))
		backfillCtx.release()
	})

	ginkgo.It("empty patch should work", func() {
		patch := &backfillPatch{}
		backfillCtx := newBackfillContext(baseBatch, patch, tableSchema, tableSchema.GetColumnDeletions(),
			tableSchema.Schema.ArchivingSortColumns, tableSchema.Schema.PrimaryKeyColumns,
			tableSchema.ValueTypeByColumn, tableSchema.DefaultValues, hostMemoryManager)
		err := backfillCtx.backfill(jobManager.reportBackfillJobDetail, jobKey)
		??(err).Should(BeNil())
		??(backfillCtx.new.Equals(&baseBatch.Batch)).Should(BeTrue())
	})

	ginkgo.It("getChangedBaseRow should work", func() {
		changedPatchRow, err := backfillCtx.getChangedPatchRow(RecordID{0, 0}, upsertBatches[0])
		??(err).Should(BeNil())
		changedBaseRow := backfillCtx.getChangedBaseRow(RecordID{0, 0}, changedPatchRow)
		??(changedBaseRow).Should(BeNil())

		changedPatchRow, err = backfillCtx.getChangedPatchRow(RecordID{0, 1}, upsertBatches[0])
		??(err).Should(BeNil())
		changedBaseRow = backfillCtx.getChangedBaseRow(RecordID{0, 1}, changedPatchRow)
		??(len(changedBaseRow)).Should(Equal(6))

		??(changedBaseRow[0].Valid).Should(BeTrue())
		??(*(*uint32)(changedBaseRow[0].OtherVal)).Should(BeEquivalentTo(1))

		??(changedBaseRow[1].Valid).Should(BeTrue())
		??(*(*uint32)(changedBaseRow[1].OtherVal)).Should(BeEquivalentTo(0))

		??(changedBaseRow[2].Valid).Should(BeTrue())
		??(*(*uint32)(changedBaseRow[2].OtherVal)).Should(BeEquivalentTo(1))

		??(changedBaseRow[3]).Should(BeNil())

		??(changedBaseRow[4].Valid).Should(BeTrue())
		??(*(*uint32)(changedBaseRow[4].OtherVal)).Should(BeEquivalentTo(1))

		??(changedBaseRow[5].Valid).Should(BeTrue())
		??(*(*uint32)(changedBaseRow[5].OtherVal)).Should(BeEquivalentTo(11))
	})

	ginkgo.It("getChangedPatchRow should work", func() {
		changedPatchRow, err := backfillCtx.getChangedPatchRow(RecordID{0, 1}, upsertBatches[0])
		??(err).Should(BeNil())
		??(len(changedPatchRow)).Should(Equal(6))

		??(changedPatchRow[0].Valid).Should(BeTrue())
		??(*(*uint32)(changedPatchRow[0].OtherVal)).Should(BeEquivalentTo(1))

		??(changedPatchRow[1].Valid).Should(BeTrue())
		??(*(*uint32)(changedPatchRow[1].OtherVal)).Should(BeEquivalentTo(0))

		??(changedPatchRow[2].Valid).Should(BeTrue())
		??(*(*uint32)(changedPatchRow[2].OtherVal)).Should(BeEquivalentTo(1))

		??(changedPatchRow[3]).Should(BeNil())

		??(changedPatchRow[3]).Should(BeNil())

		??(changedPatchRow[5].Valid).Should(BeTrue())
		??(*(*uint32)(changedPatchRow[5].OtherVal)).Should(BeEquivalentTo(11))
	})

	ginkgo.It("writePatchValueForUnsortColumn should work", func() {
		changedPatchRow, err := backfillCtx.getChangedPatchRow(RecordID{1, 1}, upsertBatches[1])
		??(err).Should(BeNil())

		??(backfillCtx.columnsForked[4]).Should(BeFalse())
		oldColumn := backfillCtx.new.Columns[4]
		backfillCtx.writePatchValueForUnsortedColumn(RecordID{0, 2}, changedPatchRow)
		??(err).Should(BeNil())
		??(backfillCtx.columnsForked[4]).Should(BeTrue())

		newValue := backfillCtx.new.GetDataValue(2, 4)
		??(*(*uint32)(newValue.OtherVal)).Should(BeEquivalentTo(12))

		forkedColumn := backfillCtx.new.Columns[4]
		??(forkedColumn).ShouldNot(Equal(oldColumn))
		changedPatchRow, err = backfillCtx.getChangedPatchRow(RecordID{1, 2}, upsertBatches[1])
		??(err).Should(BeNil())

		backfillCtx.writePatchValueForUnsortedColumn(RecordID{0, 3}, changedPatchRow)
		??(err).Should(BeNil())

		newValue = backfillCtx.new.GetDataValue(3, 4)
		??(*(*uint32)(newValue.OtherVal)).Should(BeEquivalentTo(13))

		// column should not be forked again.
		??(forkedColumn).Should(Equal(backfillCtx.new.Columns[4]))
	})

	ginkgo.It("apply backfill patch should work", func() {
		err := backfillCtx.backfill(jobManager.reportBackfillJobDetail, jobKey)
		??(err).Should(BeNil())
		??(len(backfillCtx.backfillStore.Batches)).Should(Equal(1))
		??(backfillCtx.backfillStore.NextWriteRecord).Should(Equal(RecordID{BaseBatchID, 3}))
		??(backfillCtx.columnsForked).Should(BeEquivalentTo([]bool{false, false, false, false, true, false}))
		??(backfillCtx.baseRowDeleted).Should(HaveLen(2))
		??(backfillCtx.baseRowDeleted).Should(ConsistOf(1, 4))

		// Compare result batch with expected batch.
		batch, err := getFactory().ReadLiveBatch("backfill/backfillTempLiveStore")
		??(err).Should(BeNil())
		backfillBatch := backfillCtx.backfillStore.GetBatchForRead(BaseBatchID)
		defer backfillBatch.RUnlock()
		for _, column := range backfillBatch.Columns {
			if column != nil {
				column.(*cLiveVectorParty).length = 3
			}
		}

		??(batch.Equals(&backfillBatch.Batch)).Should(BeTrue())
		??(newBatch.Equals(&backfillCtx.new.Batch)).Should(BeTrue())
	})

	ginkgo.It("createArchivingPatch should work", func() {
		err := backfillCtx.backfill(jobManager.reportBackfillJobDetail, jobKey)
		??(err).Should(BeNil())
		backfillCtx.backfillStore.AdvanceLastReadRecord()
		ap := backfillCtx.backfillStore.snapshot().createArchivingPatch(tableSchema.GetArchivingSortColumns())
		??(len(ap.data.batches)).Should(Equal(1))
		??(len(ap.recordIDs)).Should(Equal(3))
	})

	ginkgo.It("createNewArchiveStoreVersionForBackfill should work", func() {
		backfillPatches, err := createBackfillPatches(upsertBatches[:], jobManager.reportBackfillJobDetail, jobKey)
		??(err).Should(BeNil())
		err = shard.createNewArchiveStoreVersionForBackfill(backfillPatches, jobManager.reportBackfillJobDetail, jobKey)
		??(err).Should(BeNil())

		jobManager.RLock()
		jobManager.jobDetails[jobKey].LockDuration = 0
		??(*(jobManager.jobDetails[jobKey])).Should(Equal(BackfillJobDetail{
			JobDetail: JobDetail{
				Current:         1,
				Total:           1,
				NumRecords:      7,
				NumAffectedDays: 1,
			},
			Stage: "apply patch",
		}))
		jobManager.RUnlock()
	})

	ginkgo.It("disable disk purge in backfill should work", func() {
		(m.diskStore).(*diskMocks.DiskStore).Calls = []mock.Call{}
		shard.PinForPeerDataTransfer()
		defer shard.DoneWithPeerDataTransfer()
		backfillPatches, err := createBackfillPatches(upsertBatches[:], jobManager.reportBackfillJobDetail, jobKey)
		??(err).Should(BeNil())
		err = shard.createNewArchiveStoreVersionForBackfill(backfillPatches, jobManager.reportBackfillJobDetail, jobKey)
		??(err).Should(BeNil())

		jobManager.RLock()
		jobManager.jobDetails[jobKey].LockDuration = 0
		??(*(jobManager.jobDetails[jobKey])).Should(Equal(BackfillJobDetail{
			JobDetail: JobDetail{
				Current:         1,
				Total:           1,
				NumRecords:      7,
				NumAffectedDays: 1,
			},
			Stage: "apply patch",
		}))
		jobManager.RUnlock()
		??((m.diskStore).(*diskMocks.DiskStore).AssertNotCalled(utils.TestingT, "DeleteBatchVersions", table, shardID,
			0, uint32(0), uint32(0))).Should(BeTrue())
	})

	ginkgo.It("Live store with batch size of 1 should work", func() {
		backfillCtx.backfillStore.BatchSize = 1
		err := backfillCtx.backfill(jobManager.reportBackfillJobDetail, jobKey)
		??(err).Should(BeNil())
		??(len(backfillCtx.backfillStore.Batches)).Should(Equal(3))
		??(backfillCtx.backfillStore.NextWriteRecord).Should(Equal(RecordID{BaseBatchID + 3, 0}))
	})
})
