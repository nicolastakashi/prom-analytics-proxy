package db

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestQueryType_Constants(t *testing.T) {
	assert.Equal(t, QueryType("range"), QueryTypeRange)
	assert.Equal(t, QueryType("instant"), QueryTypeInstant)
}

func TestDatabaseProvider_Constants(t *testing.T) {
	assert.Equal(t, DatabaseProvider("postgresql"), PostGreSQL)
	assert.Equal(t, DatabaseProvider("sqlite"), SQLite)
}

func TestRuleUsageKind_Constants(t *testing.T) {
	assert.Equal(t, RuleUsageKind("alert"), RuleUsageKindAlert)
	assert.Equal(t, RuleUsageKind("record"), RuleUsageKindRecord)
}
