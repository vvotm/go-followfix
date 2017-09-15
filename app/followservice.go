package app

import (
	"sync"
	set "gopkg.in/fatih/set.v0"
	"fmt"
	"strconv"
	"strings"
	"log"
)

var followService *FollowService
var once *sync.Once = &sync.Once{}

type FollowService struct {
	excludeUIDSet *set.Set
	excludeAnchorIdSet *set.Set
	Lock *sync.RWMutex
	Traffic chan PeerUID
	ProduceEnd chan bool
}

type PeerUID struct {
	UID int
	FollowCnt *set.Set
	FansCnt *set.Set
}

func NewFollowService(wg *sync.WaitGroup) *FollowService {
	if followService == nil {
		once.Do(func() {
			return FollowService{&set.New(), &set.New(), wg, &sync.RWMutex{}, make(chan PeerUID, 100), make(chan bool)}
		})
	}
	return followService
}

func (followService *FollowService) Produce()  {
	wg := &sync.WaitGroup{}
	for table := 0; table < 10 ; table++  {
		tablename := USER_FOLLOW_TABLE_PREFIX + strconv.Itoa(table)
		go (func(followService *FollowService, tablename string) {
			log.Println(fmt.Sprintf("Process table 【%s】start", tablename))
			wg.Add(1)
			followService.processSplitTable(tablename)
			log.Println(fmt.Sprintf("Process table 【%s】end", tablename))
			wg.Done()
		})(followService, tablename)
	}
	wg.Wait()
	followService.ProduceEnd <- true // mark as produce end
}

func (followService *FollowService) Consumer()  {
	wg := &sync.WaitGroup{}
	for  {
		select {
		case <- followService.ProduceEnd:
			break
		case peerUID := <- followService.Traffic:
			wg.Add(1)
			go (func(wg *sync.WaitGroup, peerUID PeerUID) {
				followService.WriteDbRedis(peerUID)
				wg.Done()
			})(wg, peerUID)
		}
	}
	wg.Wait()
}

// WriteDbRedis 将单个UID用户写入到Reids中, 更新数据库
func (followService *FollowService) WriteDbRedis( peerUID PeerUID)  {


}

// processSplitTable 处理分表数据
func (followService *FollowService)processSplitTable(tablename string)  {
	dbUsersData, err := GetApp().dbmgr.GetDbByName(DB_USERS_DATA)
	defer dbUsersData.Db.Close()
	CheckErr(err)
	sql := fmt.Sprintf("select uid, anchor from %s where isFriends = 0", tablename)
	if !followService.excludeUIDSet.IsEmpty() { // exclude has process uid
		uidList := followService.excludeUIDSet.List()
		sql = fmt.Sprintf("%s and uid not in (%s)", sql, strings.Join(uidList, ','))
	}
	if !followService.excludeAnchorIdSet.IsEmpty() { // exclude has process anchor
		anchorIdList := followService.excludeAnchorIdSet.List()
		sql = fmt.Sprint("%s and anchor not in (%s)", sql, strings.Join(anchorIdList, ","))
	}
	dbRows, err := dbUsersData.Db.Query(sql)
	defer dbRows.Close()
	CheckErr(err)
	uniqueUIDSet := set.NewNonTS()
	var uid, anchor int
	for dbRows.Next() {
		dbRows.Scan(&uid, &anchor)
		followService.excludeUIDSet.Add(uid) // record
		followService.excludeAnchorIdSet.Add(anchor)
		uniqueUIDSet.Add(uid)
		uniqueUIDSet.Add(anchor)
	}

	uidChan := make(chan int, 1000)
	for {
		uid := uniqueUIDSet.Pop()
		if uid == nil {
			break
		}
		go followService.CalculateUIDFollowFansCnt(uid, uidChan)
	}
	<- uidChan
}

// CalculateUIDFollowFansCnt 计算单个UID的粉丝数, 关注数, 存放到集合中
func (followService *FollowService) CalculateUIDFollowFansCnt(uid int, uidChan chan int)  {
	dbUsersData, err := GetApp().dbmgr.GetDbByName(DB_USERS_DATA)
	defer dbUsersData.Db.Close()
	CheckErr(err)

	followCntSet := set.New()
	fansCntSet := set.New()
	var followSql, fansSql, tablename string
	var anchor int
	for index := 0; index < 10 ; index++  {
		tablename = USER_FOLLOW_TABLE_PREFIX + strconv.Itoa(index)

		followSql = fmt.Sprintf("select anchor from %s where uid = %d and isFriends = 0", tablename, uid)
		followRows, err := dbUsersData.Db.Query(followSql)
		defer followRows.Close()
		CheckErr(err)
		for followRows.Next() {
			followRows.Scan(&anchor)
			followCntSet.Add(anchor)
		}

		fansSql = fmt.Sprintf("select uid from %s where anchor = %d and isFriends = 0", tablename, uid)
		fansRows, err := dbUsersData.Db.Query(fansSql)
		defer fansRows.Close()
		CheckErr(err)
		for fansRows.Next() {
			fansRows.Scan(&anchor)
			fansCntSet.Add(anchor)
		}
	}

	peerUID := PeerUID{UID:uid, FollowCnt:followCntSet, FansCnt:fansCntSet}
	followService.Lock.Lock()
	defer followService.Lock.RUnlock()
	followService.Traffic <- peerUID
	uidChan <- 1
}

