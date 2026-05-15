package es

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strconv"

	"github.com/elastic/go-elasticsearch/esapi"

	"github.com/RedInn7/gomall/repository/db/model"
)

const ProductIndex = "product"

// productIndexMapping IK 分词器 (常用中文搜索) 如果集群没装也能跑，会退回 standard
var productIndexMapping = `{
  "settings": {
    "number_of_shards": 1,
    "number_of_replicas": 0
  },
  "mappings": {
    "properties": {
      "id":             { "type": "long" },
      "name":           { "type": "text", "analyzer": "standard" },
      "title":          { "type": "text", "analyzer": "standard" },
      "info":           { "type": "text", "analyzer": "standard" },
      "category_id":    { "type": "long" },
      "price":          { "type": "keyword" },
      "discount_price": { "type": "keyword" },
      "on_sale":        { "type": "boolean" },
      "num":            { "type": "long" },
      "boss_id":        { "type": "long" },
      "img_path":       { "type": "keyword" },
      "created_at":     { "type": "date", "format": "epoch_second" }
    }
  }
}`

// ProductDoc ES 中的商品文档结构，与 model.Product 同构但去掉了 gorm 字段
type ProductDoc struct {
	ID            uint   `json:"id"`
	Name          string `json:"name"`
	Title         string `json:"title"`
	Info          string `json:"info"`
	CategoryID    uint   `json:"category_id"`
	Price         string `json:"price"`
	DiscountPrice string `json:"discount_price"`
	OnSale        bool   `json:"on_sale"`
	Num           int    `json:"num"`
	BossID        uint   `json:"boss_id"`
	ImgPath       string `json:"img_path"`
	CreatedAt     int64  `json:"created_at"`
}

func docFromModel(p *model.Product) *ProductDoc {
	return &ProductDoc{
		ID:            p.ID,
		Name:          p.Name,
		Title:         p.Title,
		Info:          p.Info,
		CategoryID:    p.CategoryID,
		Price:         p.Price,
		DiscountPrice: p.DiscountPrice,
		OnSale:        p.OnSale,
		Num:           p.Num,
		BossID:        p.BossID,
		ImgPath:       p.ImgPath,
		CreatedAt:     p.CreatedAt.Unix(),
	}
}

// EnsureProductIndex 幂等地创建索引。已存在则跳过
func EnsureProductIndex(ctx context.Context) error {
	if EsClient == nil {
		return errors.New("es client not initialized")
	}
	exists, err := EsClient.Indices.Exists([]string{ProductIndex})
	if err != nil {
		return err
	}
	defer exists.Body.Close()
	if exists.StatusCode == 200 {
		return nil
	}
	req := esapi.IndicesCreateRequest{
		Index: ProductIndex,
		Body:  bytes.NewReader([]byte(productIndexMapping)),
	}
	res, err := req.Do(ctx, EsClient)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if res.IsError() {
		body, _ := io.ReadAll(res.Body)
		return fmt.Errorf("create index failed: %s", string(body))
	}
	return nil
}

// UpsertProduct 写入或更新一条
func UpsertProduct(ctx context.Context, p *model.Product) error {
	if EsClient == nil {
		return errors.New("es client not initialized")
	}
	doc := docFromModel(p)
	body, err := json.Marshal(doc)
	if err != nil {
		return err
	}
	req := esapi.IndexRequest{
		Index:      ProductIndex,
		DocumentID: strconv.FormatUint(uint64(p.ID), 10),
		Body:       bytes.NewReader(body),
		Refresh:    "false",
	}
	res, err := req.Do(ctx, EsClient)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if res.IsError() {
		bz, _ := io.ReadAll(res.Body)
		return fmt.Errorf("upsert failed: %s", string(bz))
	}
	return nil
}

// DeleteProduct 删除一条
func DeleteProduct(ctx context.Context, productID uint) error {
	if EsClient == nil {
		return errors.New("es client not initialized")
	}
	req := esapi.DeleteRequest{
		Index:      ProductIndex,
		DocumentID: strconv.FormatUint(uint64(productID), 10),
		Refresh:    "false",
	}
	res, err := req.Do(ctx, EsClient)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	// 404 视作幂等成功
	if res.IsError() && res.StatusCode != 404 {
		bz, _ := io.ReadAll(res.Body)
		return fmt.Errorf("delete failed: %s", string(bz))
	}
	return nil
}

// SearchProducts 多字段模糊查询，返回命中文档 + 总数
func SearchProducts(ctx context.Context, keyword string, from, size int) ([]*ProductDoc, int64, error) {
	if EsClient == nil {
		return nil, 0, errors.New("es client not initialized")
	}
	if size <= 0 {
		size = 20
	}
	q := map[string]any{
		"from": from,
		"size": size,
		"query": map[string]any{
			"multi_match": map[string]any{
				"query":  keyword,
				"fields": []string{"name^3", "title^2", "info"},
			},
		},
	}
	body, _ := json.Marshal(q)
	req := esapi.SearchRequest{
		Index: []string{ProductIndex},
		Body:  bytes.NewReader(body),
	}
	res, err := req.Do(ctx, EsClient)
	if err != nil {
		return nil, 0, err
	}
	defer res.Body.Close()
	if res.IsError() {
		bz, _ := io.ReadAll(res.Body)
		return nil, 0, fmt.Errorf("search failed: %s", string(bz))
	}

	var parsed struct {
		Hits struct {
			Total struct {
				Value int64 `json:"value"`
			} `json:"total"`
			Hits []struct {
				Source *ProductDoc `json:"_source"`
			} `json:"hits"`
		} `json:"hits"`
	}
	if err := json.NewDecoder(res.Body).Decode(&parsed); err != nil {
		return nil, 0, err
	}
	out := make([]*ProductDoc, 0, len(parsed.Hits.Hits))
	for _, h := range parsed.Hits.Hits {
		if h.Source != nil {
			out = append(out, h.Source)
		}
	}
	return out, parsed.Hits.Total.Value, nil
}
