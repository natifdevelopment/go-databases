package views

import (
	"database/sql"

	log "github.com/natifdevelopment/go-observability/logging/logger"
)

func execOrLog(db *sql.DB, query string, args ...any) {
	if _, err := db.Exec(query, args...); err != nil {
		log.Errorf("MigrateView SQL error: %v", err)
	}
}

func MigrateView(db *sql.DB) {
	// Create extension
	execOrLog(db, "CREATE EXTENSION IF NOT EXISTS pg_trgm")
	execOrLog(db, "CREATE EXTENSION IF NOT EXISTS \"uuid-ossp\"")

	// Crete indexing
	execOrLog(db, "CREATE INDEX if not exists idx_account_device_account_lastseen ON t_account_device (t_account_id, last_seen DESC)")

	// Required for dashbaord task management
	execOrLog(db, `
	create or replace view v_total_jam_login as
	WITH base AS (
	select t_account_id, 
    suM( case 
        when logged_out_date is not null 
            then logged_out_date - last_login
        else now() - last_login
    end) as selisih
	from t_account_device tad 
	group by t_account_id
),
converted AS (
    SELECT 
        t_account_id,
				EXTRACT(EPOCH FROM selisih)::int total_detik,
        CASE 
            WHEN selisih < interval '1 hour' THEN 
                CEIL(EXTRACT(EPOCH FROM selisih) / 60)::int
            WHEN selisih < interval '3 days' THEN 
                CEIL(EXTRACT(EPOCH FROM selisih) / 3600)::int
            ELSE 
                CEIL(EXTRACT(EPOCH FROM selisih) / 86400)::int
        END AS nilai,
        CASE 
            WHEN selisih < interval '1 hour' THEN 'menit'
            WHEN selisih < interval '3 days' THEN 'jam'
            ELSE 'hari'
        END AS satuan
    FROM base
)
SELECT 
    t_account_id,
		total_detik,
    nilai AS total_nilai,
    satuan,
    nilai::text || ' ' || satuan AS durasi_login
FROM converted;
`)

	// This required for rakor
	// @deprecated
	// db.Exec(`CREATE OR REPLACE VIEW v_rencana_pasokan_jadwal_kalender AS
	// 	select
	// 		t_rencana_pasokan_id,
	// 		t_rencana_pasokan_pemasok_id,
	// 		t_pemasok_id,
	// 		bongkar_pengiriman_ke,
	// 		total_pengiriman,
	// 		total_hari_bongkar,
	// 		kapasitas_bongkar,
	// 		spek_batubara,
	// 		status_jadwal,
	// 		sum(volume_harian) as total_volume,
	// 		min(tgl_bongkar) as tgl_mulai,
	// 		max(tgl_bongkar) as tgl_selesai
	// 	from
	// 		t_rencana_pasokan_jadwal trpj
	// 	group by
	// 		t_rencana_pasokan_id,
	// 		t_rencana_pasokan_pemasok_id,
	// 		t_pemasok_id,
	// 		bongkar_pengiriman_ke,
	// 		total_pengiriman,
	// 		total_hari_bongkar,
	// 		kapasitas_bongkar,
	// 		spek_batubara,
	// 		status_jadwal
	// 	order by
	// 		bongkar_pengiriman_ke asc`)

	// Required for HOP
	execOrLog(db, `CREATE OR REPLACE VIEW v_hop_jadwal_pengiriman AS
WITH rencana
     AS (SELECT trpj.t_pemasok_id,
                trp.t_organization_id AS t_pembangkit_id,
                trpj.bongkar_pengiriman_ke,
                trpp.skema_kontrak_code,
                SUM(volume_harian)    AS volume_rencana
         FROM   t_rencana_pasokan_jadwal trpj
                left join t_rencana_pasokan_pemasok trpp
                       ON trpj.t_rencana_pasokan_pemasok_id = trpp.id
                left join t_rencana_pasokan trp
                       ON trp.id = trpj.t_rencana_pasokan_id
         WHERE  trpj.deleted_at IS NULL
         GROUP  BY trpj.t_pemasok_id,
                   trp.t_organization_id,
                   trpj.bongkar_pengiriman_ke,
                   trpp.skema_kontrak_code
         ORDER  BY bongkar_pengiriman_ke ASC),
     konfirmasi
     AS (SELECT Max(id :: text)                               AS id,
                t_pembangkit_id,
                t_pemasok_id,
                bongkar_pengiriman_ke,
                Coalesce(Nullif(skema_kontrak_code, ''), 'FOB') AS
                skema_kontrak_code,
                SUM(total_volume)                             AS
                volume_konfirmasi
         FROM   t_entry_jadwal tej
         WHERE  deleted_at IS NULL
                AND status_entry_jadwal <> 'approved'
                AND bongkar_hari_ke = 1
         GROUP  BY t_pemasok_id,
                   t_pembangkit_id,
                   bongkar_pengiriman_ke,
                   Coalesce(Nullif(skema_kontrak_code, ''), 'FOB')
         ORDER  BY bongkar_pengiriman_ke ASC),
     konfirmasi_approved
     AS (SELECT Max(id :: text)                               AS id,
                t_pembangkit_id,
                t_pemasok_id,
                bongkar_pengiriman_ke,
                Coalesce(Nullif(skema_kontrak_code, ''), 'FOB') AS
                skema_kontrak_code,
                SUM(total_volume)                             AS
                   volume_konfirmasi_approved,
                Max(tgl_eta)                                  AS tgl_eta
         FROM   t_entry_jadwal tej
         WHERE  deleted_at IS NULL
                AND status_entry_jadwal = 'approved'
                AND bongkar_hari_ke = 1
         GROUP  BY t_pemasok_id,
                   t_pembangkit_id,
                   bongkar_pengiriman_ke,
                   Coalesce(Nullif(skema_kontrak_code, ''), 'FOB')
         ORDER  BY bongkar_pengiriman_ke ASC),
     jadwal
     AS (SELECT tjp.id             AS t_jadwal_pengiriman_id,
                tjp.t_entry_jadwal_id :: text,
                tjp.no_jadwal,
                tjp.skema_kontrak_code,
                tjp.t_pemasok_id,
                tjp.t_pembangkit_id,
                tlic.t_master_kapal_id,
                tjp.volume_confirm AS volume_jadwal,
                tlic.volume_bl,
                tanggal_eta
         FROM   t_jadwal_pengiriman tjp
                left join (SELECT t_jadwal_pengiriman_id,
                                  volume_bl,
                                  t_master_kapal_id
                           FROM   t_loading_info_cif tlic
                           UNION
                           SELECT tli.t_jadwal_pengiriman_id,
                                  tli.volume_bl,
                                  tr.t_master_kapal_id
                           FROM   t_loading_info_fob tli
                                  left join t_roa tr
                                         ON tli.t_jadwal_pengiriman_id =
                                            tr.t_jadwal_pengiriman_id
                           UNION
                           SELECT t_jadwal_pengiriman_id,
                                  NULL AS volume_bl,
                                  NULL AS t_master_kapal_id
                           FROM   t_loading_info_cfr tlic)tlic
                       ON tlic.t_jadwal_pengiriman_id = tjp.id
         WHERE  tjp.deleted_at IS NULL
         ORDER  BY tanggal_eta ASC,
                   skema_kontrak_code ASC),
     rakor
     AS (SELECT ka.id :: text AS t_entry_jadwal_id,
                r.*,
                k.volume_konfirmasi,
                ka.volume_konfirmasi_approved,
                ka.tgl_eta
         FROM   rencana r
                left join konfirmasi k
                       ON r.t_pembangkit_id = k.t_pembangkit_id
                          AND r.t_pemasok_id = k.t_pemasok_id
                          AND r.bongkar_pengiriman_ke = k.bongkar_pengiriman_ke
                          AND r.skema_kontrak_code = k.skema_kontrak_code
                left join konfirmasi_approved ka
                       ON r.t_pembangkit_id = ka.t_pembangkit_id
                          AND r.t_pemasok_id = ka.t_pemasok_id
                          AND r.bongkar_pengiriman_ke = ka.bongkar_pengiriman_ke
                          AND r.skema_kontrak_code = ka.skema_kontrak_code
         ORDER  BY ka.tgl_eta ASC,
                   r.skema_kontrak_code ASC)
       SELECT j.*,
              r.volume_rencana,
              r.volume_konfirmasi,
              r.volume_konfirmasi_approved
       FROM   jadwal j
              left join rakor r
                     ON j.t_entry_jadwal_id = r.t_entry_jadwal_id;
       `)

	execOrLog(db, `CREATE OR REPLACE VIEW v_dashboard_monitoring_transaksi AS
              with v_loading_info as (
                     select
                            jadwal_pengiriman.id,
                            coalesce(loading_cif.t_master_kapal_id, loading_trans.t_master_kapal_id, null) as master_kapal_id,
                            coalesce(loading_cif.t_master_tongkang_id, loading_trans.t_master_tongkang_id, null) as master_tongkang_id
                     from 	
                            (select * from t_jadwal_pengiriman where deleted_at is null ) as jadwal_pengiriman
                     left join
                            t_loading_info_cif as loading_cif
                            on
                            loading_cif.t_jadwal_pengiriman_id = jadwal_pengiriman.id
                            and jadwal_pengiriman.skema_kontrak_code = 'CIF'
                     left join
                     t_loading_info_cfr as loading_cfr
                     on
                            loading_cfr.t_jadwal_pengiriman_id = jadwal_pengiriman.id
                            and jadwal_pengiriman.skema_kontrak_code = 'CFR'
                     left join
                            t_loading_info_fob as loading_fob
                            on
                            loading_fob.t_jadwal_pengiriman_id = jadwal_pengiriman.id
                            and jadwal_pengiriman.skema_kontrak_code = 'FOB'
                     left join
                            t_loading_info_trans as loading_trans
                            on
                            loading_trans.t_jadwal_pengiriman_id = jadwal_pengiriman.id
                            and jadwal_pengiriman.skema_kontrak_code = 'TRANS'
              ), v_nor_izin_sandar_bongkar as (
                     select
                            jadwal_pengiriman.id,
                            coalesce(t_nor_izin_sandar_bongkar.ta_tgl_jam, t_nor_izin_sandar_bongkar_trans.ta_tgl_jam, t_nor_izin_sandar_bongkar_pembangkit.ta_tgl_jam, null) as ta_tgl_jam,
                            coalesce(t_nor_izin_sandar_bongkar.status, t_nor_izin_sandar_bongkar_trans.status, t_nor_izin_sandar_bongkar_pembangkit.status, null) as status
                     from 	
                            (select * from t_jadwal_pengiriman where deleted_at is null ) as jadwal_pengiriman
                     left join
                            t_nor_izin_sandar_bongkar
                            on
                            t_nor_izin_sandar_bongkar.t_jadwal_pengiriman_id = jadwal_pengiriman.id
                     left join
                            t_nor_izin_sandar_bongkar_trans
                            on
                            t_nor_izin_sandar_bongkar_trans.t_jadwal_pengiriman_id = jadwal_pengiriman.id
                     left join
                            t_nor_izin_sandar_bongkar_pembangkit
                            on
                            t_nor_izin_sandar_bongkar_pembangkit.t_jadwal_pengiriman_id = jadwal_pengiriman.id
              )
              select * from (
                     select 
                            *,
                            case
                                   when status_pembayaran = 'approved' then 'Sudah Pembayaran'
                                   when status_pembayaran = 'pending' then 'Dalam Pembayaran'
                                   when status_nota_dinas = 'approved' then 'Sudah Nota Dinas'
                                   when status_nota_dinas = 'pending' then 'Dalam Nota Dinas'
                                   when status_spt = 'approved' then 'Sudah SPT'
                                   when status_spt = 'pending' then 'Dalam SPT'
                                   when status_invoice = 'approved' then 'Sudah Invoice'
                                   when status_invoice = 'pending' then 'Dalam Invoice'
                                   when status_perhitungan = 'approved' then 'Sudah Perhitungan'
                                   when status_perhitungan = 'pending' then 'Dalam Perhitungan'
                                   when status_bongkar = 'approved' then 'Sudah Bongkar'
                                   when status_bongkar = 'pending' then 'Dalam Bongkar'
                                   when status_pengiriman = 'approved' then 'Sudah Pengiriman'
                                   when status_pengiriman = 'pending' then 'Dalam Pengiriman'
                            else
                                   'Belum Pengiriman'
                            end as keterangan
                     from (
                            select
                            jadwal_pengiriman.id,
                            jadwal_pengiriman.no_jadwal,
                            jadwal_pengiriman.no_pengiriman,
                            jadwal_pengiriman.skema_kontrak_code,
                            jadwal_pengiriman.tanggal_eta,
                            jadwal_pengiriman.deleted_at,
                            TO_CHAR(jadwal_pengiriman.periode, 'YYYY-MM') as periode,
                            pembangkit.id::text as pembangkit_id,	
                            pembangkit.name as pembangkit_name,
                            pemasok.id::text as pemasok_id,
                            pemasok.name as pemasok_name,
                            loading_info.master_kapal_id as master_kapal_id,
                            master_kapal.nama as master_kapal_name,
                            loading_info.master_tongkang_id as master_tongkang_id,
                            master_tongkang.nama as master_tongkang_name,
                            nor_izin_sandar_bongkar.ta_tgl_jam as ta_tgl_jam,
                            catat_bongkar_ds.volume_bongkar as volume_bongkar,
                            (
                                   case 
                                          when jadwal_pengiriman.skema_kontrak_code = 'CFR' and loading_info.id is not null then 'approved'
                                          when jadwal_pengiriman.skema_kontrak_code = 'FOB' and roa.id is not null and loading_info.id is not null then 'approved'
                                          when jadwal_pengiriman.skema_kontrak_code = 'FOB' and roa.id is not null and loading_info.id is null then 'pending'
                                          when loading_info.id is not null and nor_izin_sandar_bongkar.id is not null then 'approved'
                                          when loading_info.id is not null then 'pending'
                                   else
                                          'rejected'
                                   end
                            ) as status_pengiriman,
                            (
                                   case 
                                          when jadwal_pengiriman.skema_kontrak_code = 'CIF' and nor_izin_sandar_bongkar.status = 'APPROVED' and bast.id is not null then 'approved'
                                          when jadwal_pengiriman.skema_kontrak_code = 'CIF' and nor_izin_sandar_bongkar.id is not null then 'pending'
                                          when jadwal_pengiriman.skema_kontrak_code = 'CFR' and pencatatan_penerimaan_cfr.id is not null and bast.id is not null then 'approved'
                                          when jadwal_pengiriman.skema_kontrak_code = 'CFR' and loading_info.id is not null then 'pending'
                                          when jadwal_pengiriman.skema_kontrak_code = 'FOB' and roa.id is not null and loading_info.id is not null then 'approved'
				              when jadwal_pengiriman.skema_kontrak_code = 'FOB' and roa.id is not null and loading_info.id is null then 'pending'
                                          when jadwal_pengiriman.skema_kontrak_code = 'TRANS' and nor_izin_sandar_bongkar.status = 'APPROVED' and bast.id is not null then 'approved'
                                          when jadwal_pengiriman.skema_kontrak_code = 'TRANS' and nor_izin_sandar_bongkar.id is not null then 'pending'
                                   else
                                          'rejected'
                                   end
                            ) as status_bongkar,
                            (
                                   case 
                                          when bast.id is not null and coa_cow.id is not null and propose_perhitungan.id is not null and propose_perhitungan.status = 'APPROVED' then 'approved'
                                          when bast.id is not null and coa_cow.id is not null then 'pending'
                                   else
                                          'rejected'
                                   end
                            ) as status_perhitungan,
                            (
                                   case 
                                          when upload_invoice_penagihan.id is not null and permohonan_pembayaran.id is not null then 'approved'
                                          when upload_invoice_penagihan.id is not null then 'pending'
                                   else
                                          'rejected'
                                   end
                            ) as status_invoice,
                            (
                                   case 
                                          when permohonan_pembayaran.id is not null and surat_pengantar_tagihan.id is not null then 'approved'
                                          when permohonan_pembayaran.id is not null then 'pending'
                                   else
                                          'rejected'
                                   end
                            ) as status_spt,
                            (
                                   case 
                                          when surat_pengantar_tagihan.id is not null and nota_dinas.id is not null then 'approved'
                                          when surat_pengantar_tagihan.id is not null then 'pending'
                                   else
                                          'rejected'
                                   end
                            ) as status_nota_dinas,
                            (
                                   case 
                                          when nota_dinas.id is not null and pelunasan_penagihan.id is not null then 'approved'
                                          when nota_dinas.id is not null then 'pending'
                                   else
                                          'rejected'
                                   end
                            ) as status_pembayaran,
                            CURRENT_DATE - jadwal_pengiriman.due_date::date as sla_hari,
                            (
                                   case
                                          when CURRENT_DATE - jadwal_pengiriman.due_date::date > 0 then 'pending'
                                   else
                                          'rejected'
                                   end
                            ) as sla_status,
                            sla.code as sla_code,
                            sla.unit as sla
                            from 	
                                   (select * from t_jadwal_pengiriman where deleted_at is null) as jadwal_pengiriman
                            left join (
                                   select * from t_config_data where deleted_at is null
                                   ) as sla on sla.code = concat('bbo//cd/sla/', lower(jadwal_pengiriman.skema_kontrak_code))
                            left join
                                   (select id, name from t_organization where deleted_at is null) as pemasok on pemasok.id = jadwal_pengiriman.t_pemasok_id
                            left join
                                   (select id, name from t_organization where deleted_at is null) as pembangkit on pembangkit.id = jadwal_pengiriman.t_pembangkit_id
                            left join
                                   (select t_jadwal_pengiriman_id, volume_bongkar from t_catat_bongkar_ds where deleted_at is null ) as catat_bongkar_ds on catat_bongkar_ds.t_jadwal_pengiriman_id = jadwal_pengiriman.id
                            left join (
                                   select * from t_roa where deleted_at is null
                                   ) as roa on roa.t_jadwal_pengiriman_id = jadwal_pengiriman.id
                            left join (
                                   select * from t_pencatatan_penerimaan_cfr where deleted_at is null
                                   ) as pencatatan_penerimaan_cfr on pencatatan_penerimaan_cfr.t_jadwal_pengiriman_id = jadwal_pengiriman.id
                            left join v_loading_info as loading_info on
                                   loading_info.id = jadwal_pengiriman.id
                            left join 
                                   v_nor_izin_sandar_bongkar as nor_izin_sandar_bongkar on nor_izin_sandar_bongkar.id = jadwal_pengiriman.id
                            left join (
                                   select * from t_bast_cif where deleted_at is null
                                   ) as bast on bast.t_jadwal_pengiriman_id = jadwal_pengiriman.id
                            left join (
                                   select * from t_coa_cow_cif where deleted_at is null
                                   ) as coa_cow on coa_cow.t_jadwal_pengiriman_id = jadwal_pengiriman.id
                            left join (
                                   select * from t_propose_perhitungan where deleted_at is null
                                   ) as propose_perhitungan on propose_perhitungan.t_jadwal_pengiriman_id = jadwal_pengiriman.id
                            left join (
                                   select * from t_upload_invoice_penagihan where deleted_at is null
                                   ) as upload_invoice_penagihan on upload_invoice_penagihan.t_propose_perhitungan_id  = propose_perhitungan.id
                            left join (
                                   select * from t_permohonan_pembayaran where deleted_at is null
                                   ) as permohonan_pembayaran on permohonan_pembayaran.no_penagihan = propose_perhitungan.no_penagihan 
                            left join (
                                   select * from t_surat_pengantar_tagihan where deleted_at is null
                                   ) as surat_pengantar_tagihan on surat_pengantar_tagihan.t_permohonan_pembayaran_id  = permohonan_pembayaran.id
                            left join (
                                   select * from t_nota_dinas_item where deleted_at is null
                                   ) as nota_dinas_item on nota_dinas_item.t_permohonan_pembayaran_id  = permohonan_pembayaran.id
                            left join (
                                   select * from t_nota_dinas where deleted_at is null
                                   ) as nota_dinas on nota_dinas.id = nota_dinas_item.t_nota_dinas_id 
                            left join (
                                   select * from t_pelunasan_penagihan_item where deleted_at is null
                                   ) as pelunasan_penagihan_item on pelunasan_penagihan_item.t_permohonan_pembayaran_id  = permohonan_pembayaran.id
                            left join (
                                   select * from t_pelunasan_penagihan where deleted_at is null
                                   ) as pelunasan_penagihan on pelunasan_penagihan.id = pelunasan_penagihan_item.t_pelunasan_penagihan_id  
                            left join
                                   (
                                   select
                                          t_master_kapal.id,
                                          t_master_epi_kapal.nama
                                   from
                                          t_master_kapal
                                   left join t_master_epi_kapal on
                                          t_master_epi_kapal.id = t_master_kapal.t_master_epi_kapal_id
                                   where
                                          t_master_kapal.deleted_at is null
                                          and t_master_epi_kapal.deleted_at is null 
                                   ) as master_kapal on
                                   loading_info.master_kapal_id = master_kapal.id
                            left join
                                   (
                                   select
                                          t_master_tongkang.id,
                                          t_master_epi_tongkang.nama
                                   from
                                          t_master_tongkang
                                   left join t_master_epi_tongkang on
                                          t_master_epi_tongkang.id = t_master_tongkang.t_master_epi_tongkang_id
                                   where
                                          t_master_tongkang.deleted_at is null
                                          and t_master_epi_tongkang.deleted_at is null 
                                   ) as master_tongkang on
                                   loading_info.master_tongkang_id = master_tongkang.id
                     )
              )
       `)

	execOrLog(db, `CREATE OR REPLACE VIEW v_dashboard_monitoring_milestone_cif AS
              select 
                     TO_CHAR(tjp.periode, 'YYYY-MM') as periode,
                     to3.name as pemasok_a,  
                     tjp.no_jadwal as no_jadwal_b,
                     tjp.no_pengiriman as no_pengiriman_c, 
                     tjp.skema_kontrak_code, 
                     tli.kapal as kapal_d, 
                     to2.name as pembangkit_e,
                     tjp.created_at::date as tanggal_entri_jadwal_pengiriman_f,
                     tjp.tanggal_eta as tanggal_konfirmasi_g, 
                     tli.created_at::date as tanggal_entri_loading_info_dan_coa_loading_h,
                     tli.created_at::date - tjp.created_at::date as durasi_entri_loading_info_dan_coa_loading_i_h_min_f,
                     tplr.created_at::date as tanggal_approve_coa_loading_j,
                     tplr.created_at::date - tli.created_at::date as durasi_approve_coa_loading_k_j_min_h, 
                     tnisb.ta_tgl_jam::date as tanggal_nor_unloading_l,
                     tnisb.created_at::date as tanggal_entri_nor_unloading_m,
                     tnisb.created_at::date - tnisb.ta_tgl_jam::date as durasi_entri_nor_unloading_n_m_min_l,
                     tnisb.updated_at::date as tanggal_approval_nor_unloading_o, --ubah tnisb.updated_at jadi tnisb.approved.at
                     tnisb.updated_at::date - tnisb.created_at::date as durasi_approval_entri_nor_unloading_p_o_min_m, --ubah tnisb.updated_at jadi tnisb.approved.at
                     tnisb.tgl_sib::date as tanggal_izin_sandar_dan_bongkar_q,
                     tnisb.updated_at::date as tanggal_approve_izin_sandar_dan_bongkar_r, --ubah tnisb.updated_at jadi tnisb.approved.at
                     tnisb.updated_at::date - tnisb.tgl_sib::date as durasi_approve_izin_sandar_dan_bongkar_s_r_min_q, --ubah tnisb.updated_at jadi tnisb.approved.at
                     tnisb.ta_tgl_jam::date - tjp.tanggal_eta as ketepatan_jadwal_pasokan_t_l_min_g,
                     tcbd2.realisasi_sandar::date as tanggal_sandar_u,
                     tcbd2.realisasi_sandar::date - tnisb.ta_tgl_jam::date as durasi_tongkang_antri_sandar_v_u_min_t,
                     tcbd2.mulai_bongkar::date as tanggal_mulai_bongkar_w,
                     tcbd2.selesai_bongkar::date as tanggal_selesai_bongkar_x,
                     tcbd2.selesai_bongkar::date - tcbd2.mulai_bongkar::date as durasi_bongkar_y_x_min_w,
                     tcbd2.created_at::date as tanggal_entri_catat_bonngkar_dan_ds_report_z,
                     tcbd2.created_at::date - tcbd2.selesai_bongkar::date as durasi_catat_bongkar_dan_ds_report_aa_z_min_x,
                     tbc.tanggal_bast::date as tanggal_bast_ab,
                     tbc.created_at::date as tanggal_submit_bast_ac,
                     tbc.created_at::date - tbc.tanggal_bast::date as durasi_submit_bast_ad_ac_min_ab,
                     tbc.updated_at::date as tanggal_approve_bast_ae, --ubah tbc.updated_at jadi tbc.approved.at
                     tbc.updated_at::date - tbc.tanggal_bast::date as durasi_approve_bast_af_ae_min_ab, --ubah tbc.updated_at jadi tbc.approved.at
                     (case when tbc.t_dok_denda_id is not null then tbc.created_at::date end) as tanggal_ba_keterlambatan_ag,
                     (case when tbc.t_dok_denda_id is not null then tbc.updated_at::date end) as tanggal_approve_ba_keterlambatan_ah,  --ubah tbc.updated_at jadi tbc.approved.at
                     (case when tbc.t_dok_denda_id is not null then tbc.updated_at::date - tbc.tanggal_bast::date end) as durasi_approve_ba_keterlambatan_ai_ah_min_ag,  --ubah tbc.updated_at jadi tbc.approved.at
                     tccc.tanggal_coa::date as tanggal_cow_aj,
                     tccc.tanggal_coa::date as tanggal_coa_ak,
                     tccc.created_at::date as tanggal_upload_cow_al,
                     tccc.created_at::date as tanggal_upload_coa_am,
                     tccc.created_at::date - tccc.tanggal_coa::date as durasi_entri_cow_an_al_min_aj,
                     tccc.created_at::date - tccc.tanggal_coa::date as durasi_entri_coa_ao_am_min_ak,
                     tpp.created_at::date as tanggal_porpose_tagihan_ap,
                     tpp.created_at::date - tbc.updated_at::date as durasi_porpose_tagihan_aq_ap_min_ae, --ubah tbc.updated_at jadi tbc.approved.at, tpp.updated_at jadi tpp.approved.at
                     tpp.updated_at::date as tanggal_verifikasi_tagihan_ar, --ubah tpp.updated_at jadi tpp.approved.at
                     tpp.updated_at::date - tpp.created_at::date as durasi_verifikasi_tagihan_as_ar_min_ap, --ubah tpp.updated_at jadi tpp.approved.at
                     tpp.updated_at::date as tanggal_approve_tagihan_at, --ubah tpp.updated_at jadi tpp.approved.at
                     tpp.updated_at::date - tpp.created_at::date as durasi_approval_tagihan_au_at_min_ap, --ubah tpp.updated_at jadi tpp.approved.at
                     tuip.tanggal_invoice::date as tanggal_submit_invoice_av,
                     tspt.tgl_spt::date as tanggal_submit_spt_aw,
                     tspt.tgl_spt::date - tuip.tanggal_invoice::date as durasi_submit_spt_ax_aw_min_av,
                     tspt.created_at::date as tanggal_upload_spt_ay,
                     tspt.created_at::date - tuip.tanggal_invoice::date as durasi_upload_spt_az_ay_min_av,
                     tpp.updated_at::date as tanggal_approve_baph_ba, --ubah tpp.updated_at jadi tpp.approved.at
                     tpp.updated_at::date - tpp.created_at::date as durasi_approval_baph_bb_ba_min_ap, --ubah tpp.updated_at jadi tpp.approved.at
                     tpp2.tgl_kirim_dok_fisik_pln::date as tanggal_kirim_dokumen_tagihan_ke_bdh_bc,
                     tpp2.tgl_kirim_dok_fisik_pln::date - tspt.created_at::date as durasi_kirim_dokumen_tagihan_ke_bdh_bd_bc_min_ay,
                     tnd.tgl_nota_dinas::date as tanggal_nota_dinas_be,
                     tnd.tgl_nota_dinas::date - tuip.tanggal_invoice::date as durasi_pemrosesan_nota_dinas_bf_be_min_av,
                     tvp.tgl_validasi_dok_fisik::date as tanggal_verifikasi_bdh_bg,
                     tvp.tgl_validasi_dok_fisik::date - tnd.tgl_nota_dinas::date as durasi_verifikasi_bdh_bh_bg_min_be,
                     tpp3.tgl_pembayaran::date as tangal_bayar_bi,
                     tpp3.tgl_pembayaran::date - tvp.tgl_validasi_dok_fisik::date as durasi_bayar_bj_bi_min_bg,
                     tpp3.tgl_pembayaran::date - tcbd2.created_at::date as durasi_proses_tagihan_bk_bi_min_z
              from t_jadwal_pengiriman tjp 
              left join t_organization to2 on tjp.t_pembangkit_id = to2.id 
              left join t_organization to3 on tjp.t_pemasok_id = to3.id
              left join (select tli.*, tmek.nama as kapal from 
              (select tli.t_jadwal_pengiriman_id, tr.t_master_kapal_id, tli.created_at from t_loading_info_fob tli 
              left join t_roa tr on tr.t_jadwal_pengiriman_id = tli.t_jadwal_pengiriman_id
              where tli.status in ('APPROVED', 'PUBLISHED') and tli.deleted_at  is null
              union
              select t_jadwal_pengiriman_id, t_master_kapal_id,created_at from t_loading_info_cif tlic 
              where status in ('APPROVED', 'PUBLISHED') and deleted_at  is null
              union
              select t_jadwal_pengiriman_id, t_master_kapal_id,created_at from t_loading_info_trans tlit
              where status in ('APPROVED', 'PUBLISHED') and deleted_at  is null
              union
              select t_jadwal_pengiriman_id, null as t_master_kapal_id,created_at from t_loading_info_cfr tlic
              where status in ('APPROVED', 'PUBLISHED') and deleted_at  is null) tli
              left join t_master_kapal tmk on tli.t_master_kapal_id = tmk.id
              left join t_master_epi_kapal tmek on tmk.t_master_epi_kapal_id = tmek.id)tli on tli.t_jadwal_pengiriman_id = tjp.id
              left join
              (select t_jadwal_pengiriman_id, volume_bongkar as volume_ds from t_catat_bongkar_ds tcbd 
              union
              select t_jadwal_pengiriman_id, sum(volume_penyerahan) as volume_ds from t_pencatatan_penerimaan_cfr tppc 
              group by t_jadwal_pengiriman_id)tcbd on tcbd.t_jadwal_pengiriman_id = tjp.id
              left join t_coa_loading tcl on tcl.t_jadwal_pengiriman_id = tjp.id
              left join t_psa_loading_roa tplr on tplr.t_jadwal_pengiriman_id = tjp.id
              left join t_nor_izin_sandar_bongkar tnisb  on tjp.id = tnisb.t_jadwal_pengiriman_id 
              left join t_catat_bongkar_ds tcbd2 on tjp.id = tcbd2.t_jadwal_pengiriman_id
              left join t_bast_cif tbc on tbc.t_jadwal_pengiriman_id = tjp.id
              left join t_coa_cow_cif tccc on tccc.t_jadwal_pengiriman_id = tjp.id
              left join t_propose_perhitungan tpp on tpp.t_jadwal_pengiriman_id = tjp.id
              left join t_upload_invoice_penagihan tuip on tpp.id = tuip.t_propose_perhitungan_id 
              left join t_permohonan_pembayaran_item tppi on tpp.id = tppi.t_propose_perhitungan_id
              left join t_permohonan_pembayaran tpp2 on tppi.t_permohonan_pembayaran_id = tpp2.id
              left join t_surat_pengantar_tagihan tspt on tspt.t_permohonan_pembayaran_id = tpp2.id 
              left join t_nota_dinas_item tndi on tndi.t_permohonan_pembayaran_id = tpp2.id 
              left join t_nota_dinas tnd on tnd.id = tndi.t_nota_dinas_id 
              left join t_verifikasi_penagihan tvp on tvp.t_propose_perhitungan_id = tpp.id 
              left join t_pelunasan_penagihan_item tppi2 on tppi2.t_permohonan_pembayaran_id = tppi.id 
              left join t_pelunasan_penagihan tpp3 on tpp3.id = tppi2.t_pelunasan_penagihan_id 
              where tjp.skema_kontrak_code = 'CIF'
       `)

	execOrLog(db, `CREATE OR REPLACE VIEW v_dashboard_monitoring_milestone_fob AS
              select 
                     TO_CHAR(tjp.periode, 'YYYY-MM') as periode,
                     to3.name as pemasok_a,  
                     tjp.no_jadwal as no_jadwal_b,
                     tjp.no_pengiriman as no_pengiriman_c, 
                     tjp.skema_kontrak_code, 
                     tli.kapal as kapal_d, 
                     to2.name as pembangkit_e,
                     tjp.created_at::date as tanggal_entri_jadwal_pengiriman_f,
                     tjp.target_loading as tanggal_target_loading_g, 
                     tr.created_at::date as tanggal_entri_roa_h,
                     tr.updated_at::date as tanggal_approval_roa_i, --ubah tr.updated_at jadi tr.approved.at
                     tr.updated_at::date - tr.created_at::date as durasi_entri_roa_j_i_min_h, --ubah tr.updated_at jadi tr.approved.at
                     tli.created_at::date as tanggal_entri_loading_info_k,
                     tli.mulai_loading::date as tanggal_mulai_loading_l,
                     tli.selesai_loading::date as tanggal_selsesai_loading_m,
                     tli.selesai_loading::date - tli.mulai_loading::date as durasi_loading_n_m_min_l,
                     tli.selesai_loading::date - tjp.target_loading as ketepatan_target_loading_o_m_min_g,
                     tbc.tanggal_bast::date as tanggal_bast_p,
                     tbc.created_at::date as tanggal_submit_bast_q,
                     tbc.created_at::date - tbc.tanggal_bast::date as durasi_submit_bast_r_q_min_p,
                     tbc.updated_at::date as tanggal_approve_bast_s, --ubah tbc.updated_at jadi tbc.approved.at
                     tbc.updated_at::date - tbc.tanggal_bast::date as durasi_approve_bast_t_s_min_p, --ubah tbc.updated_at jadi tbc.approved.at
                     (case when tbc.t_dok_denda_id is not null then tbc.created_at::date end) as tanggal_ba_keterlambatan_u,
                     (case when tbc.t_dok_denda_id is not null then tbc.updated_at::date end) as tanggal_approve_ba_keterlambatan_v,  --ubah tbc.updated_at jadi tbc.approved.at
                     (case when tbc.t_dok_denda_id is not null then tbc.updated_at::date - tbc.tanggal_bast::date end) as durasi_approve_ba_keterlambatan_w_v_min_p,  --ubah tbc.updated_at jadi tbc.approved.at
                     tccc.tanggal_coa::date as tanggal_cow_x,
                     tccc.tanggal_coa::date as tanggal_coa_y,
                     tccc.created_at::date as tanggal_upload_cow_z,
                     tccc.created_at::date as tanggal_upload_coa_aa,
                     tccc.created_at::date - tccc.tanggal_coa::date as durasi_entri_cow_ab_z_min_x,
                     tccc.created_at::date - tccc.tanggal_coa::date as durasi_entri_coa_ac_aa_min_y,
                     tpp.created_at::date as tanggal_porpose_tagihan_ad,
                     tpp.created_at::date - tbc.updated_at::date as durasi_porpose_tagihan_ae_ad_min_s, --ubah tbc.updated_at jadi tbc.approved.at, tpp.updated_at jadi tpp.approved.at
                     tpp.updated_at::date as tanggal_verifikasi_tagihan_af, --ubah tpp.updated_at jadi tpp.approved.at
                     tpp.updated_at::date - tpp.created_at::date as durasi_verifikasi_tagihan_ag_af_min_ad, --ubah tpp.updated_at jadi tpp.approved.at
                     tpp.updated_at::date as tanggal_approve_tagihan_ah, --ubah tpp.updated_at jadi tpp.approved.at
                     tpp.updated_at::date - tpp.created_at::date as durasi_approval_tagihan_ai_ah_min_ad, --ubah tpp.updated_at jadi tpp.approved.at
                     tuip.tanggal_invoice::date as tanggal_submit_invoice_aj,
                     tspt.tgl_spt::date as tanggal_submit_spt_ak,
                     tspt.tgl_spt::date - tuip.tanggal_invoice::date as durasi_submit_spt_al_ak_min_aj,
                     tspt.created_at::date as tanggal_upload_spt_am,
                     tspt.created_at::date - tuip.tanggal_invoice::date as durasi_upload_spt_an_am_min_aj,
                     tpp.updated_at::date as tanggal_approve_baph_ao, --ubah tpp.updated_at jadi tpp.approved.at
                     tpp.updated_at::date - tpp.created_at::date as durasi_approval_baph_ap_ao_min_ad, --ubah tpp.updated_at jadi tpp.approved.at
                     tpp2.tgl_kirim_dok_fisik_pln::date as tanggal_kirim_dokumen_tagihan_ke_bdh_aq,
                     tpp2.tgl_kirim_dok_fisik_pln::date - tspt.created_at::date as durasi_kirim_dokumen_tagihan_ke_bdh_ar_aq_min_am,
                     tnd.tgl_nota_dinas::date as tanggal_nota_dinas_as,
                     tnd.tgl_nota_dinas::date - tuip.tanggal_invoice::date as durasi_pemrosesan_nota_dinas_at_as_min_aj,
                     tvp.tgl_validasi_dok_fisik::date as tanggal_verifikasi_bdh_au,
                     tvp.tgl_validasi_dok_fisik::date - tnd.tgl_nota_dinas::date as durasi_verifikasi_bdh_av_au_min_as,
                     tpp3.tgl_pembayaran::date as tangal_bayar_aw,
                     tpp3.tgl_pembayaran::date - tvp.tgl_validasi_dok_fisik::date as durasi_bayar_ax_aw_min_au,
                     tpp3.tgl_pembayaran::date - tli.selesai_loading::date as durasi_proses_tagihan_ay_aw_min_m
              from t_jadwal_pengiriman tjp 
              left join t_organization to2 on tjp.t_pembangkit_id = to2.id 
              left join t_organization to3 on tjp.t_pemasok_id = to3.id
              left join (select tli.*, tmek.nama as kapal from 
              (select tli.t_jadwal_pengiriman_id, tr.t_master_kapal_id, tli.created_at, tli.selesai_muat as selesai_loading, tli.tgl_muat as mulai_loading from t_loading_info_fob tli 
              left join t_roa tr on tr.t_jadwal_pengiriman_id = tli.t_jadwal_pengiriman_id
              where tli.status in ('APPROVED', 'PUBLISHED') and tli.deleted_at  is null
              union
              select t_jadwal_pengiriman_id, t_master_kapal_id,created_at,selesai_loading, mulai_loading from t_loading_info_cif tlic 
              where status in ('APPROVED', 'PUBLISHED') and deleted_at  is null
              union
              select t_jadwal_pengiriman_id, t_master_kapal_id,created_at,selesai_muat as selesai_loading, tgl_muat as mulai_loading from t_loading_info_trans tlit
              where status in ('APPROVED', 'PUBLISHED') and deleted_at  is null) tli
              left join t_master_kapal tmk on tli.t_master_kapal_id = tmk.id
              left join t_master_epi_kapal tmek on tmk.t_master_epi_kapal_id = tmek.id)tli on tli.t_jadwal_pengiriman_id = tjp.id
              left join
              (select t_jadwal_pengiriman_id, volume_bongkar as volume_ds from t_catat_bongkar_ds tcbd 
              union
              select t_jadwal_pengiriman_id, sum(volume_penyerahan) as volume_ds from t_pencatatan_penerimaan_cfr tppc 
              group by t_jadwal_pengiriman_id)tcbd on tcbd.t_jadwal_pengiriman_id = tjp.id
              left join t_roa tr on tr.t_jadwal_pengiriman_id = tjp.id
              left join t_nor_izin_sandar_bongkar tnisb  on tjp.id = tnisb.t_jadwal_pengiriman_id 
              left join t_catat_bongkar_ds tcbd2 on tjp.id = tcbd2.t_jadwal_pengiriman_id
              left join t_bast_cif tbc on tbc.t_jadwal_pengiriman_id = tjp.id
              left join t_coa_cow_cif tccc on tccc.t_jadwal_pengiriman_id = tjp.id
              left join t_propose_perhitungan tpp on tpp.t_jadwal_pengiriman_id = tjp.id
              left join t_upload_invoice_penagihan tuip on tpp.id = tuip.t_propose_perhitungan_id 
              left join t_permohonan_pembayaran_item tppi on tpp.id = tppi.t_propose_perhitungan_id
              left join t_permohonan_pembayaran tpp2 on tppi.t_permohonan_pembayaran_id = tpp2.id
              left join t_surat_pengantar_tagihan tspt on tspt.t_permohonan_pembayaran_id = tpp2.id 
              left join t_nota_dinas_item tndi on tndi.t_permohonan_pembayaran_id = tpp2.id 
              left join t_nota_dinas tnd on tnd.id = tndi.t_nota_dinas_id 
              left join t_verifikasi_penagihan tvp on tvp.t_propose_perhitungan_id = tpp.id 
              left join t_pelunasan_penagihan_item tppi2 on tppi2.t_permohonan_pembayaran_id = tppi.id 
              left join t_pelunasan_penagihan tpp3 on tpp3.id = tppi2.t_pelunasan_penagihan_id 
              where tjp.skema_kontrak_code = 'FOB'
       `)

	execOrLog(db, `CREATE OR REPLACE VIEW v_dashboard_monitoring_milestone_cfr AS
              select 
                     TO_CHAR(tjp.periode, 'YYYY-MM') as periode,
                     to3.name as pemasok_a,  
                     tjp.no_jadwal as no_jadwal_b,
                     tjp.no_pengiriman as no_pengiriman_c, 
                     tjp.skema_kontrak_code, 
                     tli.kapal as kapal_d, 
                     to2.name as pembangkit_e,
                     tjp.created_at::date as tanggal_entri_jadwal_pengiriman_f,
                     tjp.tanggal_eta as tanggal_konfirmasi_g,
                     tr.created_at::date as tanggal_entri_roa_h,
                     tarf.created_at::date as tanggal_approve_roa_i,
                     tarf.created_at::date - tr.created_at::date as durasi_approve_roa_j_i_min_h,
                     tpcc2.tgl_mulai_penyerahan::date as tanggal_mulai_bongkar_k,
                     tpcc2.tgl_selesai_penyerahan::date as tanggal_selesai_bongkar_l,
                     tpcc2.tgl_selesai_penyerahan::date - tpcc2.tgl_mulai_penyerahan::date as durasi_bongkar_m_l_min_k, 
                     tpcc2.tgl_selesai_penyerahan::date - tjp.tanggal_eta as ketepatan_jadwal_pasokan_n_l_min_g,
                     tbc.tanggal_bast::date as tanggal_bast_o,
                     tbc.created_at::date as tanggal_submit_bast_p,
                     tbc.created_at::date - tbc.tanggal_bast::date as durasi_submit_bast_q_p_min_o,
                     tbc.updated_at::date as tanggal_approve_bast_r, --ubah tbc.updated_at jadi tbc.approved.at
                     tbc.updated_at::date - tbc.tanggal_bast::date as durasi_approve_bast_s_r_min_o, --ubah tbc.updated_at jadi tbc.approved.at
                     (case when tbc.t_dok_denda_id is not null then tbc.created_at::date end) as tanggal_ba_keterlambatan_t,
                     (case when tbc.t_dok_denda_id is not null then tbc.updated_at::date end) as tanggal_approve_ba_keterlambatan_u,  --ubah tbc.updated_at jadi tbc.approved.at
                     (case when tbc.t_dok_denda_id is not null then tbc.updated_at::date - tbc.tanggal_bast::date end) as durasi_approve_ba_keterlambatan_v_u_min_o,  --ubah tbc.updated_at jadi tbc.approved.at
                     tccc.tanggal_coa::date as tanggal_cow_w,
                     tccc.tanggal_coa::date as tanggal_coa_x,
                     tccc.created_at::date as tanggal_upload_cow_y,
                     tccc.created_at::date as tanggal_upload_coa_z,
                     tccc.created_at::date - tccc.tanggal_coa::date as durasi_entri_cow_aa_y_min_w,
                     tccc.created_at::date - tccc.tanggal_coa::date as durasi_entri_coa_ab_z_min_x,
                     tpp.created_at::date as tanggal_porpose_tagihan_ac,
                     tpp.created_at::date - tbc.updated_at::date as durasi_porpose_tagihan_ad_ac_min_r, --ubah tbc.updated_at jadi tbc.approved.at, tpp.updated_at jadi tpp.approved.at
                     tpp.updated_at::date as tanggal_verifikasi_tagihan_ae, --ubah tpp.updated_at jadi tpp.approved.at
                     tpp.updated_at::date - tpp.created_at::date as durasi_verifikasi_tagihan_af_ae_min_ac, --ubah tpp.updated_at jadi tpp.approved.at
                     tpp.updated_at::date as tanggal_approve_tagihan_ag, --ubah tpp.updated_at jadi tpp.approved.at
                     tpp.updated_at::date - tpp.created_at::date as durasi_approval_tagihan_ah_at_min_ac, --ubah tpp.updated_at jadi tpp.approved.at
                     tuip.tanggal_invoice::date as tanggal_submit_invoice_ai,
                     tspt.tgl_spt::date as tanggal_submit_spt_aj,
                     tspt.tgl_spt::date - tuip.tanggal_invoice::date as durasi_submit_spt_ak_aj_min_ai,
                     tspt.created_at::date as tanggal_upload_spt_al,
                     tspt.created_at::date - tuip.tanggal_invoice::date as durasi_upload_spt_am_ay_min_ai,
                     tpp.updated_at::date as tanggal_approve_baph_an, --ubah tpp.updated_at jadi tpp.approved.at
                     tpp.updated_at::date - tpp.created_at::date as durasi_approval_baph_ao_an_min_ac, --ubah tpp.updated_at jadi tpp.approved.at
                     tpp2.tgl_kirim_dok_fisik_pln::date as tanggal_kirim_dokumen_tagihan_ke_bdh_ap,
                     tpp2.tgl_kirim_dok_fisik_pln::date - tspt.created_at::date as durasi_kirim_dokumen_tagihan_ke_bdh_aq_ap_min_al,
                     tnd.tgl_nota_dinas::date as tanggal_nota_dinas_ar,
                     tnd.tgl_nota_dinas::date - tuip.tanggal_invoice::date as durasi_pemrosesan_nota_dinas_as_ar_min_ai,
                     tvp.tgl_validasi_dok_fisik::date as tanggal_verifikasi_bdh_at,
                     tvp.tgl_validasi_dok_fisik::date - tnd.tgl_nota_dinas::date as durasi_verifikasi_bdh_au_at_min_ar,
                     tpp3.tgl_pembayaran::date as tangal_bayar_av,
                     tpp3.tgl_pembayaran::date - tvp.tgl_validasi_dok_fisik::date as durasi_bayar_aw_av_min_at,
                     tpp3.tgl_pembayaran::date - tpcc2.tgl_selesai_penyerahan::date as durasi_proses_tagihan_ax_av_min_l
              from t_jadwal_pengiriman tjp 
              left join t_organization to2 on tjp.t_pembangkit_id = to2.id 
              left join t_organization to3 on tjp.t_pemasok_id = to3.id
              left join (select tli.*, tmek.nama as kapal from 
              (select tli.t_jadwal_pengiriman_id, tr.t_master_kapal_id, tli.created_at from t_loading_info_fob tli 
              left join t_roa tr on tr.t_jadwal_pengiriman_id = tli.t_jadwal_pengiriman_id
              where tli.status in ('APPROVED', 'PUBLISHED') and tli.deleted_at  is null
              union
              select t_jadwal_pengiriman_id, null as t_master_kapal_id,created_at from t_loading_info_cfr tlic
              where status in ('APPROVED', 'PUBLISHED') and deleted_at  is null) tli
              left join t_master_kapal tmk on tli.t_master_kapal_id = tmk.id
              left join t_master_epi_kapal tmek on tmk.t_master_epi_kapal_id = tmek.id)tli on tli.t_jadwal_pengiriman_id = tjp.id
              left join
              (select t_jadwal_pengiriman_id, volume_bongkar as volume_ds from t_catat_bongkar_ds tcbd 
              union
              select t_jadwal_pengiriman_id, sum(volume_penyerahan) as volume_ds from t_pencatatan_penerimaan_cfr tppc 
              group by t_jadwal_pengiriman_id)tcbd on tcbd.t_jadwal_pengiriman_id = tjp.id
              left join t_roa tr on tr.t_jadwal_pengiriman_id = tjp.id
              left join (SELECT 
              t_jadwal_pengiriman_id,
              MIN(tgl_mulai_penyerahan) AS tgl_mulai_penyerahan,
              MAX(tgl_selesai_penyerahan) AS tgl_selesai_penyerahan
              FROM t_pencatatan_penerimaan_cfr
              GROUP BY t_jadwal_pengiriman_id) tpcc2 on tjp.id = tpcc2.t_jadwal_pengiriman_id
              left join t_bast_cif tbc on tbc.t_jadwal_pengiriman_id = tjp.id
              left join t_approval_roa_fob tarf on tarf.t_jadwal_pengiriman_id = tjp.id
              left join t_coa_cow_cif tccc on tccc.t_jadwal_pengiriman_id = tjp.id
              left join t_propose_perhitungan tpp on tpp.t_jadwal_pengiriman_id = tjp.id
              left join t_upload_invoice_penagihan tuip on tpp.id = tuip.t_propose_perhitungan_id 
              left join t_permohonan_pembayaran_item tppi on tpp.id = tppi.t_propose_perhitungan_id
              left join t_permohonan_pembayaran tpp2 on tppi.t_permohonan_pembayaran_id = tpp2.id
              left join t_surat_pengantar_tagihan tspt on tspt.t_permohonan_pembayaran_id = tpp2.id 
              left join t_nota_dinas_item tndi on tndi.t_permohonan_pembayaran_id = tpp2.id 
              left join t_nota_dinas tnd on tnd.id = tndi.t_nota_dinas_id 
              left join t_verifikasi_penagihan tvp on tvp.t_propose_perhitungan_id = tpp.id 
              left join t_pelunasan_penagihan_item tppi2 on tppi2.t_permohonan_pembayaran_id = tppi.id 
              left join t_pelunasan_penagihan tpp3 on tpp3.id = tppi2.t_pelunasan_penagihan_id 
              where tjp.skema_kontrak_code = 'CFR'
       `)

	execOrLog(db, `CREATE OR REPLACE VIEW v_dashboard_monitoring_milestone_trans AS
              select 
                     TO_CHAR(tjp.periode, 'YYYY-MM') as periode,
                     to3.name as transportir_a,  
                     tjp.no_jadwal as no_jadwal_b,
                     tjp2.no_pengiriman as no_pengiriman_c, 
                     tjp.skema_kontrak_code, 
                     tli.kapal as kapal_d, 
                     to2.name as pembangkit_e,
                     tjp.created_at::date as tanggal_entri_jadwal_pengiriman_f,
                     tjp.tanggal_eta as tanggal_konfirmasi_g, 
                     tli.created_at::date as tanggal_entri_loading_info_h,
                     tli.selesai_muat::date as tanggal_selesai_loading_i,
                     tnl.created_at::date as tanggal_approve_loading_info_j,
                     tnl.created_at::date - tli.created_at::date as durasi_approve_loading_info_k,
                     tnisb.ta_tgl_jam::date as tanggal_nor_unloading_l,
                     tnisb.created_at::date as tanggal_entri_nor_unloading_m,
                     tnisb.created_at::date - tnisb.ta_tgl_jam::date as durasi_entri_nor_unloading_n_m_min_l,
                     tnisb.updated_at::date as tanggal_approval_nor_unloading_o, --ubah tnisb.updated_at jadi tnisb.approved.at
                     tnisb.updated_at::date - tnisb.created_at::date as durasi_approval_entri_nor_unloading_p_o_min_m, --ubah tnisb.updated_at jadi tnisb.approved.at
                     tnisb.tgl_sib::date as tanggal_izin_sandar_dan_bongkar_q,
                     tnisb.updated_at::date as tanggal_approve_izin_sandar_dan_bongkar_r, --ubah tnisb.updated_at jadi tnisb.approved.at
                     tnisb.updated_at::date - tnisb.tgl_sib::date as durasi_approve_izin_sandar_dan_bongkar_s_r_min_q, --ubah tnisb.updated_at jadi tnisb.approved.at
                     tnisb.ta_tgl_jam::date - tjp.tanggal_eta as ketepatan_jadwal_pasokan_t_l_min_g,
                     tcbd2.realisasi_sandar::date as tanggal_sandar_u,
                     tcbd2.realisasi_sandar::date - tnisb.ta_tgl_jam::date as durasi_tongkang_antri_sandar_v_u_min_t,
                     tcbd2.mulai_bongkar::date as tanggal_mulai_bongkar_w,
                     tcbd2.selesai_bongkar::date as tanggal_selesai_bongkar_x,
                     tcbd2.selesai_bongkar::date - tcbd2.mulai_bongkar::date as durasi_bongkar_y_x_min_w,
                     tcbd2.created_at::date as tanggal_entri_catat_bonngkar_dan_ds_report_z,
                     tcbd2.created_at::date - tcbd2.selesai_bongkar::date as durasi_catat_bongkar_dan_ds_report_aa_z_min_x,
                     tbc.tanggal_bast::date as tanggal_bast_ab,
                     tbc.created_at::date as tanggal_submit_bast_ac,
                     tbc.created_at::date - tbc.tanggal_bast::date as durasi_submit_bast_ad_ac_min_ab,
                     tbc.updated_at::date as tanggal_approve_bast_ae, --ubah tbc.updated_at jadi tbc.approved.at
                     tbc.updated_at::date - tbc.tanggal_bast::date as durasi_approve_bast_af_ae_min_ab, --ubah tbc.updated_at jadi tbc.approved.at
                     (case when tbc.t_dok_denda_id is not null then tbc.created_at::date end) as tanggal_ba_keterlambatan_ag,
                     (case when tbc.t_dok_denda_id is not null then tbc.updated_at::date end) as tanggal_approve_ba_keterlambatan_ah,  --ubah tbc.updated_at jadi tbc.approved.at
                     (case when tbc.t_dok_denda_id is not null then tbc.updated_at::date - tbc.tanggal_bast::date end) as durasi_approve_ba_keterlambatan_ai_ah_min_ag,  --ubah tbc.updated_at jadi tbc.approved.at
                     tccc.tanggal_coa::date as tanggal_cow_aj,
                     tccc.tanggal_coa::date as tanggal_coa_ak,
                     tccc.created_at::date as tanggal_upload_cow_al,
                     tccc.created_at::date as tanggal_upload_coa_am,
                     tccc.created_at::date - tccc.tanggal_coa::date as durasi_entri_cow_an_al_min_aj,
                     tccc.created_at::date - tccc.tanggal_coa::date as durasi_entri_coa_ao_am_min_ak,
                     tpp.created_at::date as tanggal_porpose_tagihan_ap,
                     tpp.created_at::date - tbc.updated_at::date as durasi_porpose_tagihan_aq_ap_min_ae, --ubah tbc.updated_at jadi tbc.approved.at, tpp.updated_at jadi tpp.approved.at
                     tpp.updated_at::date as tanggal_verifikasi_tagihan_ar, --ubah tpp.updated_at jadi tpp.approved.at
                     tpp.updated_at::date - tpp.created_at::date as durasi_verifikasi_tagihan_as_ar_min_ap, --ubah tpp.updated_at jadi tpp.approved.at
                     tpp.updated_at::date as tanggal_approve_tagihan_at, --ubah tpp.updated_at jadi tpp.approved.at
                     tpp.updated_at::date - tpp.created_at::date as durasi_approval_tagihan_au_at_min_ap, --ubah tpp.updated_at jadi tpp.approved.at
                     tuip.tanggal_invoice::date as tanggal_submit_invoice_av,
                     tspt.tgl_spt::date as tanggal_submit_spt_aw,
                     tspt.tgl_spt::date - tuip.tanggal_invoice::date as durasi_submit_spt_ax_aw_min_av,
                     tspt.created_at::date as tanggal_upload_spt_ay,
                     tspt.created_at::date - tuip.tanggal_invoice::date as durasi_upload_spt_az_ay_min_av,
                     tpp.updated_at::date as tanggal_approve_baph_ba, --ubah tpp.updated_at jadi tpp.approved.at
                     tpp.updated_at::date - tpp.created_at::date as durasi_approval_baph_bb_ba_min_ap, --ubah tpp.updated_at jadi tpp.approved.at
                     tpp2.tgl_kirim_dok_fisik_pln::date as tanggal_kirim_dokumen_tagihan_ke_bdh_bc,
                     tpp2.tgl_kirim_dok_fisik_pln::date - tspt.created_at::date as durasi_kirim_dokumen_tagihan_ke_bdh_bd_bc_min_ay,
                     tnd.tgl_nota_dinas::date as tanggal_nota_dinas_be,
                     tnd.tgl_nota_dinas::date - tuip.tanggal_invoice::date as durasi_pemrosesan_nota_dinas_bf_be_min_av,
                     tvp.tgl_validasi_dok_fisik::date as tanggal_verifikasi_bdh_bg,
                     tvp.tgl_validasi_dok_fisik::date - tnd.tgl_nota_dinas::date as durasi_verifikasi_bdh_bh_bg_min_be,
                     tpp3.tgl_pembayaran::date as tangal_bayar_bi,
                     tpp3.tgl_pembayaran::date - tvp.tgl_validasi_dok_fisik::date as durasi_bayar_bj_bi_min_bg,
                     tpp3.tgl_pembayaran::date - tcbd2.created_at::date as durasi_proses_tagihan_bk_bi_min_z
              from t_jadwal_pengiriman tjp 
              left join t_organization to2 on tjp.t_pembangkit_id = to2.id 
              left join t_jadwal_pengiriman tjp2 on tjp.t_jadwal_pengiriman_fob_id = tjp2.id
              left join t_organization to3 on tjp.t_transportir_id = to3.id
              left join (select tli.*, tmek.nama as kapal from 
              (select tli.t_jadwal_pengiriman_id, tr.t_master_kapal_id, tli.created_at, tli.selesai_muat from t_loading_info_fob tli 
              left join t_roa tr on tr.t_jadwal_pengiriman_id = tli.t_jadwal_pengiriman_id
              where tli.status in ('APPROVED', 'PUBLISHED') and tli.deleted_at  is null
              union
              select t_jadwal_pengiriman_id, t_master_kapal_id,created_at,selesai_muat  from t_loading_info_trans tlit
              where status in ('APPROVED', 'PUBLISHED') and deleted_at  is null) tli
              left join t_master_kapal tmk on tli.t_master_kapal_id = tmk.id
              left join t_master_epi_kapal tmek on tmk.t_master_epi_kapal_id = tmek.id)tli on tli.t_jadwal_pengiriman_id = tjp.id
              left join
              (select t_jadwal_pengiriman_id, volume_bongkar as volume_ds from t_catat_bongkar_ds tcbd 
              union
              select t_jadwal_pengiriman_id, sum(volume_penyerahan) as volume_ds from t_pencatatan_penerimaan_cfr tppc 
              group by t_jadwal_pengiriman_id)tcbd on tcbd.t_jadwal_pengiriman_id = tjp.id
              left join t_nor_loading tnl on tnl.t_jadwal_pengiriman_id = tjp.id
              left join t_coa_loading tcl on tcl.t_jadwal_pengiriman_id = tjp.id
              left join t_psa_loading_roa tplr on tplr.t_jadwal_pengiriman_id = tjp.id
              left join t_nor_izin_sandar_bongkar tnisb  on tjp.id = tnisb.t_jadwal_pengiriman_id 
              left join t_catat_bongkar_ds tcbd2 on tjp.id = tcbd2.t_jadwal_pengiriman_id
              left join t_bast_cif tbc on tbc.t_jadwal_pengiriman_id = tjp.id
              left join t_coa_cow_cif tccc on tccc.t_jadwal_pengiriman_id = tjp.id
              left join t_propose_perhitungan tpp on tpp.t_jadwal_pengiriman_id = tjp.id
              left join t_upload_invoice_penagihan tuip on tpp.id = tuip.t_propose_perhitungan_id 
              left join t_permohonan_pembayaran_item tppi on tpp.id = tppi.t_propose_perhitungan_id
              left join t_permohonan_pembayaran tpp2 on tppi.t_permohonan_pembayaran_id = tpp2.id
              left join t_surat_pengantar_tagihan tspt on tspt.t_permohonan_pembayaran_id = tpp2.id
              left join t_nota_dinas_item tndi on tndi.t_permohonan_pembayaran_id = tpp2.id 
              left join t_nota_dinas tnd on tnd.id = tndi.t_nota_dinas_id 
              left join t_verifikasi_penagihan tvp on tvp.t_propose_perhitungan_id = tpp.id 
              left join t_pelunasan_penagihan_item tppi2 on tppi2.t_permohonan_pembayaran_id = tppi.id 
              left join t_pelunasan_penagihan tpp3 on tpp3.id = tppi2.t_pelunasan_penagihan_id 
              where tjp.skema_kontrak_code = 'TRANS'
       `)

	execOrLog(db, `CREATE OR REPLACE VIEW v_report_monitoring_pasokan_batubara AS
              with v_loading_info as (
                     select
                            jadwal_pengiriman.id,
                            coalesce(loading_cif.t_master_kapal_id, loading_trans.t_master_kapal_id, null) as master_kapal_id,
                            coalesce(loading_cif.t_master_tongkang_id, loading_trans.t_master_tongkang_id, null) as master_tongkang_id,
                            coalesce(loading_trans.sandar, loading_fob.sandar, null) as tgl_sandar
                     from 	
                            (select * from t_jadwal_pengiriman where deleted_at is null ) as jadwal_pengiriman
                     left join
                            t_loading_info_cif as loading_cif
                            on
                            loading_cif.t_jadwal_pengiriman_id = jadwal_pengiriman.id
                            and jadwal_pengiriman.skema_kontrak_code = 'CIF'
                     left join
                     t_loading_info_cfr as loading_cfr
                     on
                            loading_cfr.t_jadwal_pengiriman_id = jadwal_pengiriman.id
                            and jadwal_pengiriman.skema_kontrak_code = 'CFR'
                     left join
                            t_loading_info_fob as loading_fob
                            on
                            loading_fob.t_jadwal_pengiriman_id = jadwal_pengiriman.id
                            and jadwal_pengiriman.skema_kontrak_code = 'FOB'
                     left join
                            t_loading_info_trans as loading_trans
                            on
                            loading_trans.t_jadwal_pengiriman_id = jadwal_pengiriman.id
                            and jadwal_pengiriman.skema_kontrak_code = 'TRANS'
              ), v_nor_izin_sandar_bongkar as (
                     select
                            jadwal_pengiriman.id,
                            coalesce(t_nor_izin_sandar_bongkar.ta_tgl_jam, t_nor_izin_sandar_bongkar_trans.ta_tgl_jam, t_nor_izin_sandar_bongkar_pembangkit.ta_tgl_jam, null) as ta_tgl_jam,
                            coalesce(t_nor_izin_sandar_bongkar.status, t_nor_izin_sandar_bongkar_trans.status, t_nor_izin_sandar_bongkar_pembangkit.status, null) as status
                     from 	
                            (select * from t_jadwal_pengiriman where deleted_at is null ) as jadwal_pengiriman
                     left join
                            t_nor_izin_sandar_bongkar
                            on
                            t_nor_izin_sandar_bongkar.t_jadwal_pengiriman_id = jadwal_pengiriman.id
                     left join
                            t_nor_izin_sandar_bongkar_trans
                            on
                            t_nor_izin_sandar_bongkar_trans.t_jadwal_pengiriman_id = jadwal_pengiriman.id
                     left join
                            t_nor_izin_sandar_bongkar_pembangkit
                            on
                            t_nor_izin_sandar_bongkar_pembangkit.t_jadwal_pengiriman_id = jadwal_pengiriman.id
              ), v_coa_cow as (
                     select
                            jadwal_pengiriman.id,
                            coalesce(coa_cow_cif.no_sertifikat_coa, coa_cow_fob.no_sertifikat_coa, coa_cow_trans.no_sertifikat_coa) as no_sertifikat_coa,
                            coalesce(coa_cow_cif.tanggal_coa, coa_cow_fob.tanggal_coa, coa_cow_trans.tanggal_coa) as tanggal_coa,
                            coalesce(coa_cow_cif.gross_calorific_value_ar, coa_cow_fob.gross_calorific_value_ar, coa_cow_trans.gross_calorific_value_ar) as gcv,
                            coalesce(coa_cow_cif.hard_grove_grindability_index, coa_cow_fob.hard_grove_grindability_index, coa_cow_trans.hard_grove_grindability_index) as hgi,
                            coalesce(coa_cow_cif.total_moisture_ar, coa_cow_fob.total_moisture_ar, coa_cow_trans.total_moisture_ar) as tm,
                            coalesce(coa_cow_cif.ash_content_ar, coa_cow_fob.ash_content_ar, coa_cow_trans.ash_content_ar) as ash,
                            coalesce(coa_cow_cif.sodium_content_in_ash, coa_cow_fob.sodium_content_in_ash, coa_cow_trans.sodium_content_in_ash) as sodium,
                            coalesce(null) as ts,
                            coalesce(coa_cow_cif.nitrogen_daf, coa_cow_fob.nitrogen_daf, coa_cow_trans.nitrogen_daf) as nitrogen,
                            coalesce(coa_cow_cif.slagging, coa_cow_fob.slagging, coa_cow_trans.slagging) as slagging,
                            coalesce(coa_cow_cif.fouling_index, coa_cow_fob.fouling_index, coa_cow_trans.fouling_index) as fouling,
                            coalesce(coa_cow_cif.ukuran_butiran_lolos_ayakan238mm, coa_cow_fob.ukuran_butiran_lolos_ayakan238mm, coa_cow_trans.ukuran_butiran_lolos_ayakan238mm) as ayakan238mm,
                            coalesce(coa_cow_cif.ukuran_butiran_lolos_ayakan32mm, coa_cow_fob.ukuran_butiran_lolos_ayakan32mm, coa_cow_trans.ukuran_butiran_lolos_ayakan32mm) as ayakan32mm,
                            coalesce(coa_cow_cif.ukuran_butiran_lolos_ayakan50mm, coa_cow_fob.ukuran_butiran_lolos_ayakan50mm, coa_cow_trans.ukuran_butiran_lolos_ayakan50mm) as ayakan50mm,
                            coalesce(coa_cow_cif.ukuran_butiran_lolos_ayakan70mm, coa_cow_fob.ukuran_butiran_lolos_ayakan70mm, coa_cow_trans.ukuran_butiran_lolos_ayakan70mm) as ayakan70mm,
                            coalesce(coa_cow_cif.ash_fusion_temprature_idt, coa_cow_fob.ash_fusion_temprature_idt, coa_cow_trans.ash_fusion_temprature_idt) as aft
                     from 	
                            (select * from t_jadwal_pengiriman where deleted_at is null ) as jadwal_pengiriman
                     left join
                     t_coa_cow_cif as coa_cow_cif
                     on
                            coa_cow_cif.t_jadwal_pengiriman_id = jadwal_pengiriman.id
                            and jadwal_pengiriman.skema_kontrak_code = 'CIF'
                     left join
                            t_coa_cow_fob as coa_cow_fob
                            on
                            coa_cow_fob.t_jadwal_pengiriman_id = jadwal_pengiriman.id
                            and jadwal_pengiriman.skema_kontrak_code = 'FOB'
                     left join
                            t_coa_cow_trans as coa_cow_trans
                            on
                            coa_cow_trans.t_jadwal_pengiriman_id = jadwal_pengiriman.id
                            and jadwal_pengiriman.skema_kontrak_code = 'TRANS'
              )
              select * from (
                     select 
                            *
                     from (
                            select
                            jadwal_pengiriman.id,
                            jadwal_pengiriman.periode,
                            pembangkit.id::text as pembangkit_id,
                            pembangkit.name as pembangkit,
                            pemasok.id::text as pemasok_id,
                            pemasok.name as pemasok,
                            loading_info.master_kapal_id as master_kapal_id,
                            master_kapal.nama as master_kapal,
                            nor_izin_sandar_bongkar.ta_tgl_jam as tgl_nor,
                            coalesce(loading_info.tgl_sandar, pencatatan_penerimaan_cfr.tgl_selesai_penyerahan, psa_loading_roa.td) as tgl_sandar,
                            coalesce(bypass_bongkar.selesai_bongkar, catat_bongkar_ds.selesai_bongkar, ba_bongkar_trans.selesai_bongkar) as tgl_selesai_bongkar,
                            coalesce(bypass_bongkar.volume_bongkar, catat_bongkar_ds.volume_bongkar) as volume_ds,
                            bast.tanggal_bast as tgl_bast,
              --		coa_cow.*
                            coa_cow.no_sertifikat_coa,
                            coa_cow.tanggal_coa as tgl_coa,
                            coa_cow.gcv,
                            coa_cow.hgi,
                            coa_cow.tm,
                            coa_cow.ash,
                            coa_cow.sodium,
                            coa_cow.ts,
                            coa_cow.nitrogen,
                            coa_cow.slagging,
                            coa_cow.fouling,
                            coa_cow.ayakan238mm,
                            coa_cow.ayakan32mm,
                            coa_cow.ayakan50mm,
                            coa_cow.ayakan70mm,
                            coa_cow.aft
                            from 	
                                   (select * from t_jadwal_pengiriman where deleted_at is null) as jadwal_pengiriman
                            left join (
                                   select * from t_config_data where deleted_at is null
                                   ) as sla on sla.code = concat('bbo//cd/sla/', lower(jadwal_pengiriman.skema_kontrak_code))
                            left join
                                   (select id, name from t_organization where deleted_at is null) as pemasok on pemasok.id = jadwal_pengiriman.t_pemasok_id
                            left join
                                   (select id, name from t_organization where deleted_at is null) as pembangkit on pembangkit.id = jadwal_pengiriman.t_pembangkit_id
                            left join
                                   (select * from t_catat_bongkar_ds where deleted_at is null ) as catat_bongkar_ds on catat_bongkar_ds.t_jadwal_pengiriman_id = jadwal_pengiriman.id
                            left join
                                   (select * from t_bypass_bongkar where deleted_at is null ) as bypass_bongkar on bypass_bongkar.t_jadwal_pengiriman_id = jadwal_pengiriman.id
                            left join
                                   (select * from t_ba_bongkar_trans where deleted_at is null ) as ba_bongkar_trans on ba_bongkar_trans.t_jadwal_pengiriman_id = jadwal_pengiriman.id
                            left join (
                                   select * from t_psa_loading_roa where deleted_at is null
                                   ) as psa_loading_roa on psa_loading_roa.t_jadwal_pengiriman_id = jadwal_pengiriman.id
                            left join (
                                   select * from t_pencatatan_penerimaan_cfr where deleted_at is null
                                   ) as pencatatan_penerimaan_cfr on pencatatan_penerimaan_cfr.t_jadwal_pengiriman_id = jadwal_pengiriman.id
                            left join v_loading_info as loading_info on
                                   loading_info.id = jadwal_pengiriman.id
                            left join 
                                   v_nor_izin_sandar_bongkar as nor_izin_sandar_bongkar on nor_izin_sandar_bongkar.id = jadwal_pengiriman.id
                            left join (
                                   select * from t_bast_cif where deleted_at is null
                                   ) as bast on bast.t_jadwal_pengiriman_id = jadwal_pengiriman.id
                            left join v_coa_cow as coa_cow on coa_cow.id = jadwal_pengiriman.id
                            left join
                                   (
                                   select
                                          t_master_kapal.id,
                                          t_master_epi_kapal.nama
                                   from
                                          t_master_kapal
                                   left join t_master_epi_kapal on
                                          t_master_epi_kapal.id = t_master_kapal.t_master_epi_kapal_id
                                   where
                                          t_master_kapal.deleted_at is null
                                          and t_master_epi_kapal.deleted_at is null 
                                   ) as master_kapal on
                                   loading_info.master_kapal_id = master_kapal.id
                     )
              )
       `)

	log.Println("DB views created successfully!")
}
