use sea_orm::entity::prelude::*;
use serde::{Deserialize, Serialize};

#[derive(Clone, Debug, PartialEq, DeriveEntityModel, Serialize, Deserialize)]
#[sea_orm(table_name = "tournaments")]
pub struct Model {
    #[sea_orm(primary_key, auto_increment = false)]
    pub id: Uuid,
    pub name: String,
    pub status: String,
    pub duration_ticks: i32,
    pub ticks_left: i32,
    #[sea_orm(column_type = "Decimal(Some((10, 6)))")]
    pub gini_start: Decimal,
    #[sea_orm(column_type = "Decimal(Some((10, 6)))", nullable)]
    pub gini_final: Option<Decimal>,
    pub created_at: DateTimeUtc,
    pub started_at: Option<DateTimeUtc>,
    pub finished_at: Option<DateTimeUtc>,
}

#[derive(Copy, Clone, Debug, EnumIter, DeriveRelation)]
pub enum Relation {}

impl ActiveModelBehavior for ActiveModel {}
